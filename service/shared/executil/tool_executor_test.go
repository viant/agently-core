package executil

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/runtime/memory"
)

func TestResolveToolStatus_DataDriven(t *testing.T) {
	type testCase struct {
		name     string
		err      error
		parentFn func() context.Context
		expected string
	}
	cases := []testCase{
		{name: "success", err: nil, parentFn: context.Background, expected: "completed"},
		{name: "canceled-by-parent", err: nil, parentFn: func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }, expected: "canceled"},
		{name: "exec-error", err: assert.AnError, parentFn: context.Background, expected: "failed"},
		{name: "exec-canceled", err: context.Canceled, parentFn: context.Background, expected: "canceled"},
		{name: "exec-deadline", err: context.DeadlineExceeded, parentFn: context.Background, expected: "canceled"},
	}
	for _, tc := range cases {
		ctx := tc.parentFn()
		got, _ := resolveToolStatus(tc.err, ctx)
		assert.EqualValues(t, tc.expected, got, tc.name)
	}
}

func TestToolExecContext_Timeout(t *testing.T) {
	// 50ms timeout
	_ = os.Setenv("AGENTLY_TOOLCALL_TIMEOUT", "50ms")
	defer os.Unsetenv("AGENTLY_TOOLCALL_TIMEOUT")
	ctx := context.Background()
	execCtx, cancel := toolExecContext(ctx)
	defer cancel()
	select {
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected timeout before 200ms")
	case <-execCtx.Done():
		// expected
		assert.Error(t, execCtx.Err())
	}
}

func TestMaybePersistSystemDocuments(t *testing.T) {
	turn := memory.TurnMeta{ConversationID: "c1", TurnID: "t1", Assistant: "agent-test"}
	result := `{"documents":[{"uri":"workspace://localhost/sys/doc.md","score":0.91},{"uri":"workspace://localhost/user/doc.md","score":0.42}]}`
	conv := &stubConv{}
	reg := newStubRegistry(map[string]string{
		"root-sys|doc.md":  "# System Playbook\nDo X\n",
		"root-user|doc.md": "user notes",
	})
	assert.NoError(t, persistDocumentsIfNeeded(context.Background(), reg, conv, turn, "resources.matchDocuments", result))
	require.Len(t, conv.insertedMessages, 2)
	msg := conv.insertedMessages[0]
	assert.Equal(t, "system", msg.Role)
	assert.Equal(t, "c1", msg.ConversationID)
	assert.Contains(t, derefString(msg.Content), "System Playbook")
	msgUser := conv.insertedMessages[1]
	assert.Equal(t, "user", msgUser.Role)
	assert.Contains(t, derefString(msgUser.Content), "user notes")

	// metadata patch for system doc
	var meta *apiconv.MutableMessage
	for _, patched := range conv.patchedMessages {
		if patched != nil && strings.EqualFold(derefString(patched.Mode), SystemDocumentMode) {
			meta = patched
			break
		}
	}
	require.NotNil(t, meta)
	assert.Equal(t, SystemDocumentMode, derefString(meta.Mode))
	assert.Contains(t, strings.Split(derefString(meta.Tags), ","), SystemDocumentTag)
	assert.Equal(t, "workspace://localhost/sys/doc.md", derefString(meta.ContextSummary))

	conv2 := &stubConv{}
	assert.NoError(t, persistDocumentsIfNeeded(context.Background(), reg, conv2, turn, "resources.matchDocuments", `{"documents":[{"uri":"workspace://localhost/unknown/foo","score":0.1}]}`))
	assert.Len(t, conv2.insertedMessages, 0)

	conv3 := &stubConv{}
	assert.NoError(t, persistDocumentsIfNeeded(context.Background(), reg, conv3, turn, "resources.matchdocuments", "false"))
	assert.Len(t, conv3.insertedMessages, 0)

	convHyphen := &stubConv{}
	assert.NoError(t, persistDocumentsIfNeeded(context.Background(), reg, convHyphen, turn, "resources-matchDocuments", result))
	assert.Len(t, convHyphen.insertedMessages, 2)
}

func TestExecuteToolStep_RetryBehavior(t *testing.T) {
	step := StepInfo{
		ID:         "call-1",
		Name:       "flaky.tool",
		Args:       map[string]interface{}{"foo": "bar"},
		ResponseID: "resp-1",
	}
	cases := []struct {
		name              string
		script            []scriptedResult
		expectedAttempts  int
		expectError       bool
		thresholdOverride time.Duration
	}{
		{
			name: "retry-on-context-canceled",
			script: []scriptedResult{
				{result: "", err: context.Canceled},
				{result: `{"status":"ok"}`, err: nil},
			},
			expectedAttempts: 2,
			expectError:      false,
		},
		{
			name: "no-retry-on-non-context-error",
			script: []scriptedResult{
				{result: "", err: fmt.Errorf("invalid request")},
			},
			expectedAttempts: 1,
			expectError:      true,
		},
		{
			name: "no-retry-when-duration-exceeds-threshold",
			script: []scriptedResult{
				{result: "", err: context.Canceled, delay: 20 * time.Millisecond},
			},
			expectedAttempts:  1,
			expectError:       true,
			thresholdOverride: 10 * time.Millisecond,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			turn := memory.TurnMeta{ConversationID: "c-retry", TurnID: "t-retry", ParentMessageID: "p-retry"}
			ctx := memory.WithTurnMeta(context.Background(), turn)
			conv := &stubConv{}
			reg := &scriptedRegistry{script: tc.script}
			if tc.thresholdOverride > 0 {
				original := maxRetryDuration
				maxRetryDuration = tc.thresholdOverride
				t.Cleanup(func() { maxRetryDuration = original })
			}
			call, _, err := ExecuteToolStep(ctx, reg, step, conv)
			assert.EqualValues(t, tc.expectedAttempts, reg.calls)
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			if len(tc.script) > 0 {
				attemptIdx := reg.calls - 1
				if attemptIdx >= len(tc.script) {
					attemptIdx = len(tc.script) - 1
				}
				expectedResult := tc.script[attemptIdx].result
				if strings.TrimSpace(expectedResult) == "" && tc.script[attemptIdx].err != nil {
					expectedResult = tc.script[attemptIdx].err.Error()
				}
				assert.EqualValues(t, expectedResult, call.Result)
			}
		})
	}
}

func TestExecuteToolStep_ForceTerminalCloseWhenCompleteWriteFails(t *testing.T) {
	cases := []struct {
		name           string
		script         []scriptedResult
		expectedStatus string
		errContains    string
	}{
		{
			name: "completed fallback",
			script: []scriptedResult{
				{result: `{"status":"ok"}`},
			},
			expectedStatus: "completed",
		},
		{
			name: "canceled fallback",
			script: []scriptedResult{
				{err: context.Canceled},
				{err: context.Canceled},
			},
			expectedStatus: "canceled",
		},
		{
			name: "failed fallback carries return error",
			script: []scriptedResult{
				{err: fmt.Errorf("invalid request")},
			},
			expectedStatus: "failed",
			errContains:    "execute tool",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			turn := memory.TurnMeta{ConversationID: "c-force", TurnID: "t-force", ParentMessageID: "p-force"}
			ctx := memory.WithTurnMeta(context.Background(), turn)
			step := StepInfo{
				ID:         "call-force",
				Name:       "flaky.tool",
				Args:       map[string]interface{}{"foo": "bar"},
				ResponseID: "resp-force",
			}
			conv := &stubConv{
				failPatchToolCallAt: map[int]error{
					3: fmt.Errorf("simulated terminal write failure"),
				},
			}
			reg := &scriptedRegistry{script: tc.script}

			_, _, err := ExecuteToolStep(ctx, reg, step, conv)
			require.Error(t, err)
			require.GreaterOrEqual(t, conv.patchToolCallCount, 3)
			require.NotEmpty(t, conv.patchedToolCalls)

			last := conv.patchedToolCalls[len(conv.patchedToolCalls)-1]
			require.NotNil(t, last)
			assert.EqualValues(t, tc.expectedStatus, strings.ToLower(strings.TrimSpace(last.Status)))
			require.NotNil(t, last.CompletedAt)
			if tc.errContains != "" {
				require.NotNil(t, last.ErrorMessage)
				assert.Contains(t, strings.ToLower(strings.TrimSpace(*last.ErrorMessage)), strings.ToLower(strings.TrimSpace(tc.errContains)))
			}
		})
	}
}

func TestExecuteToolStep_PersistsReadImageAsAttachment(t *testing.T) {
	turn := memory.TurnMeta{ConversationID: "c-img", TurnID: "t-img", ParentMessageID: "p-img", Assistant: "agent-test"}
	ctx := memory.WithTurnMeta(context.Background(), turn)
	reg := &scriptedRegistry{script: []scriptedResult{{
		result: `{"uri":"file:///tmp/img.png","mimeType":"image/png","dataBase64":"AQID","name":"img.png"}`,
	}}}
	conv := &stubConv{}

	step := StepInfo{
		ID:         "call-img",
		Name:       "resources.readImage",
		Args:       map[string]interface{}{"path": "img.png"},
		ResponseID: "resp-1",
	}
	_, _, err := ExecuteToolStep(ctx, reg, step, conv)
	require.NoError(t, err)

	var sawToolResponse bool
	for _, p := range conv.patchedPayloads {
		if p == nil || p.Has == nil || !p.Has.Kind {
			continue
		}
		if p.Kind != "tool_response" {
			continue
		}
		sawToolResponse = true
		if p.InlineBody != nil {
			body := string(*p.InlineBody)
			assert.EqualValues(t, false, strings.Contains(body, "AQID"))
			assert.EqualValues(t, true, strings.Contains(body, "\"dataBase64Omitted\""))
		}
	}
	assert.EqualValues(t, true, sawToolResponse)

	var sawAttachmentPayload bool
	for _, p := range conv.patchedPayloads {
		if p == nil || p.Has == nil || !p.Has.Kind {
			continue
		}
		if p.Kind == "model_request" && strings.EqualFold(p.MimeType, "image/png") {
			sawAttachmentPayload = true
			if p.InlineBody != nil {
				assert.EqualValues(t, []byte{1, 2, 3}, []byte(*p.InlineBody))
			}
		}
	}
	assert.EqualValues(t, true, sawAttachmentPayload)

	var sawLink bool
	for _, m := range conv.patchedMessages {
		if m == nil || m.Has == nil || !m.Has.AttachmentPayloadID {
			continue
		}
		sawLink = true
		break
	}
	assert.EqualValues(t, true, sawLink)
}

func TestExecuteToolStep_PersistsDecodedWrappedToolResponse(t *testing.T) {
	turn := memory.TurnMeta{ConversationID: "c-wrap", TurnID: "t-wrap", ParentMessageID: "p-wrap", Assistant: "agent-test"}
	ctx := memory.WithTurnMeta(context.Background(), turn)
	body := `{"status":"ok","items":[1,2,3]}`
	wrapped, err := json.Marshal(map[string]string{
		"InlineBody":  gzipStringValue(t, body),
		"Compression": "gzip",
	})
	require.NoError(t, err)
	reg := &scriptedRegistry{script: []scriptedResult{{
		result: string(wrapped),
	}}}
	conv := &stubConv{}

	step := StepInfo{
		ID:         "call-wrap",
		Name:       "resources.grepFiles",
		Args:       map[string]interface{}{"path": "/repo"},
		ResponseID: "resp-wrap",
	}
	_, _, err = ExecuteToolStep(ctx, reg, step, conv)
	require.NoError(t, err)

	var persisted string
	for _, p := range conv.patchedPayloads {
		if p == nil || p.Has == nil || !p.Has.Kind || p.Kind != "tool_response" || p.InlineBody == nil {
			continue
		}
		persisted = string(*p.InlineBody)
	}
	require.NotEmpty(t, persisted)
	assert.EqualValues(t, "tool response payload could not be decoded", persisted)
}

func TestExecuteToolStep_CanonicalizesToolNameAndPersistsRunMeta(t *testing.T) {
	turn := memory.TurnMeta{ConversationID: "c-tool", TurnID: "t-tool", ParentMessageID: "p-tool"}
	ctx := memory.WithTurnMeta(context.Background(), turn)
	ctx = memory.WithRunMeta(ctx, memory.RunMeta{RunID: "run-tool", Iteration: 3})
	reg := &scriptedRegistry{script: []scriptedResult{{result: `{"status":"ok"}`}}}
	conv := &stubConv{}

	step := StepInfo{
		ID:         "call-tool",
		Name:       "system_os-getEnv",
		Args:       map[string]interface{}{"names": []string{"USER"}},
		ResponseID: "resp-1",
	}
	_, _, err := ExecuteToolStep(ctx, reg, step, conv)
	require.NoError(t, err)
	require.NotEmpty(t, conv.patchedMessages)
	require.NotEmpty(t, conv.patchedToolCalls)

	var toolMsg *apiconv.MutableMessage
	for _, msg := range conv.patchedMessages {
		if msg != nil && strings.EqualFold(msg.Role, "tool") {
			toolMsg = msg
			break
		}
	}
	require.NotNil(t, toolMsg)
	require.NotNil(t, toolMsg.ToolName)
	assert.Equal(t, "system/os/getEnv", *toolMsg.ToolName)
	require.NotNil(t, toolMsg.Iteration)
	assert.EqualValues(t, 3, *toolMsg.Iteration)

	started := conv.patchedToolCalls[0]
	assert.Equal(t, "system/os/getEnv", started.ToolName)
	require.NotNil(t, started.RunID)
	assert.Equal(t, "run-tool", *started.RunID)
	require.NotNil(t, started.Iteration)
	assert.EqualValues(t, 3, *started.Iteration)
}

func TestExecuteToolStep_PersistsToolMessageNameAndStatus(t *testing.T) {
	turn := memory.TurnMeta{ConversationID: "c-tool", TurnID: "t-tool", ParentMessageID: "p-tool"}
	ctx := memory.WithTurnMeta(context.Background(), turn)
	reg := &scriptedRegistry{script: []scriptedResult{{result: `{"value":"adrianwitas"}`}}}
	conv := &stubConv{}

	step := StepInfo{
		ID:         "call-tool",
		Name:       "system_os-getEnv",
		Args:       map[string]interface{}{"names": []string{"USER"}},
		ResponseID: "resp-tool",
	}
	_, _, err := ExecuteToolStep(ctx, reg, step, conv)
	require.NoError(t, err)
	require.NotEmpty(t, conv.patchedMessages)

	var sawRunningInsert bool
	var sawCompletedPatch bool
	var toolMsgID string
	for _, msg := range conv.patchedMessages {
		if msg == nil {
			continue
		}
		if derefString(msg.ToolName) != "system/os/getEnv" {
			if toolMsgID == "" || msg.Id != toolMsgID {
				continue
			}
		}
		if derefString(msg.Status) == "running" {
			sawRunningInsert = true
			toolMsgID = msg.Id
		}
		if toolMsgID != "" && msg.Id == toolMsgID && derefString(msg.Status) == "completed" {
			sawCompletedPatch = true
		}
	}

	assert.True(t, sawRunningInsert, "expected tool_op insert with running status and tool name")
	assert.True(t, sawCompletedPatch, "expected tool_op patch with terminal completed status")
}

type stubConv struct {
	patchedMessages  []*apiconv.MutableMessage
	insertedMessages []*apiconv.MutableMessage
	patchedPayloads  []*apiconv.MutablePayload
	patchedToolCalls []*apiconv.MutableToolCall
	patchedConvs     []*apiconv.MutableConversation

	patchToolCallCount     int
	patchConversationCount int

	failPatchToolCallAt     map[int]error
	failPatchConversationAt map[int]error
}

func (s *stubConv) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return nil, nil
}

func (s *stubConv) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}

func (s *stubConv) PatchConversations(_ context.Context, conv *apiconv.MutableConversation) error {
	s.patchConversationCount++
	s.patchedConvs = append(s.patchedConvs, conv)
	if s.failPatchConversationAt != nil {
		if err, ok := s.failPatchConversationAt[s.patchConversationCount]; ok {
			return err
		}
	}
	return nil
}

func (s *stubConv) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	return nil, nil
}

func (s *stubConv) PatchPayload(_ context.Context, payload *apiconv.MutablePayload) error {
	s.patchedPayloads = append(s.patchedPayloads, payload)
	return nil
}

func (s *stubConv) PatchMessage(_ context.Context, message *apiconv.MutableMessage) error {
	s.patchedMessages = append(s.patchedMessages, message)
	if message != nil && strings.TrimSpace(derefString(message.Content)) != "" {
		s.insertedMessages = append(s.insertedMessages, message)
	}
	return nil
}

func (s *stubConv) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}

func (s *stubConv) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	return nil, nil
}

func (s *stubConv) PatchModelCall(context.Context, *apiconv.MutableModelCall) error {
	return nil
}

func (s *stubConv) PatchToolCall(_ context.Context, call *apiconv.MutableToolCall) error {
	s.patchToolCallCount++
	s.patchedToolCalls = append(s.patchedToolCalls, call)
	if s.failPatchToolCallAt != nil {
		if err, ok := s.failPatchToolCallAt[s.patchToolCallCount]; ok {
			return err
		}
	}
	return nil
}

func (s *stubConv) PatchTurn(context.Context, *apiconv.MutableTurn) error {
	return nil
}

func (s *stubConv) DeleteConversation(context.Context, string) error {
	return nil
}

func (s *stubConv) DeleteMessage(context.Context, string, string) error {
	return nil
}

func derefString(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

type stubRegistry struct {
	mu        sync.Mutex
	documents map[string]string
}

type scriptedResult struct {
	result string
	err    error
	delay  time.Duration
}

type scriptedRegistry struct {
	mu       sync.Mutex
	script   []scriptedResult
	calls    int
	lastArgs map[string]interface{}
	lastName string
}

func newStubRegistry(documents map[string]string) *stubRegistry {
	return &stubRegistry{
		documents: documents,
	}
}

func (s *stubRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	canonical := strings.ToLower(strings.ReplaceAll(name, "_", "."))
	switch canonical {
	case "resources.roots", "resources-roots":
		return `{"roots":[{"id":"root-sys","uri":"workspace://localhost/sys","role":"system"},{"id":"root-user","uri":"workspace://localhost/user","role":"user"}]}`, nil
	case "resources.read", "resources-read":
		rootID := fmt.Sprint(args["rootId"])
		path := fmt.Sprint(args["path"])
		if path == "" && args["uri"] != nil {
			path = fmt.Sprint(args["uri"])
		}
		key := fmt.Sprintf("%s|%s", rootID, path)
		s.mu.Lock()
		content := s.documents[key]
		s.mu.Unlock()
		return fmt.Sprintf(`{"content":%q}`, content), nil
	default:
		return "", fmt.Errorf("unexpected tool: %s", name)
	}
}

func (s *stubRegistry) Definitions() []llm.ToolDefinition                { return nil }
func (s *stubRegistry) MatchDefinition(string) []*llm.ToolDefinition     { return nil }
func (s *stubRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) { return nil, false }
func (s *stubRegistry) MustHaveTools([]string) ([]llm.Tool, error)       { return nil, nil }
func (s *stubRegistry) SetDebugLogger(io.Writer)                         {}
func (s *stubRegistry) Initialize(context.Context)                       {}

func (s *scriptedRegistry) Execute(_ context.Context, name string, args map[string]interface{}) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastName = name
	if args != nil {
		cloned := make(map[string]interface{}, len(args))
		for k, v := range args {
			cloned[k] = v
		}
		s.lastArgs = cloned
	}
	if len(s.script) == 0 {
		s.calls++
		return "", nil
	}
	index := s.calls
	s.calls++
	if index >= len(s.script) {
		index = len(s.script) - 1
	}
	entry := s.script[index]
	delay := entry.delay
	if delay > 0 {
		time.Sleep(delay)
	}
	return entry.result, entry.err
}

func (s *scriptedRegistry) Definitions() []llm.ToolDefinition                { return nil }
func (s *scriptedRegistry) MatchDefinition(string) []*llm.ToolDefinition     { return nil }
func (s *scriptedRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) { return nil, false }
func (s *scriptedRegistry) MustHaveTools([]string) ([]llm.Tool, error)       { return nil, nil }
func (s *scriptedRegistry) SetDebugLogger(io.Writer)                         {}
func (s *scriptedRegistry) Initialize(context.Context)                       {}

func gzipStringValue(t *testing.T, value string) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write([]byte(value)); err != nil {
		t.Fatalf("gzip write failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close failed: %v", err)
	}
	return buffer.String()
}

func TestExecuteToolStep_InheritsContextWorkdir(t *testing.T) {
	turn := memory.TurnMeta{ConversationID: "c-workdir", TurnID: "t-workdir", ParentMessageID: "p-workdir"}
	ctx := memory.WithTurnMeta(context.Background(), turn)
	ctx = WithWorkdir(ctx, "/tmp/workdir")
	reg := &scriptedRegistry{script: []scriptedResult{{result: `{"status":"ok"}`}}}
	conv := &stubConv{}
	step := StepInfo{
		ID:         "call-workdir",
		Name:       "system_exec-execute",
		Args:       map[string]interface{}{"commands": []string{"pwd"}},
		ResponseID: "resp-workdir",
	}

	_, _, err := ExecuteToolStep(ctx, reg, step, conv)
	require.NoError(t, err)
	require.NotNil(t, reg.lastArgs)
	assert.EqualValues(t, "/tmp/workdir", reg.lastArgs["workdir"])
}
