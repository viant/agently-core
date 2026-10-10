package toolexec

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/tool"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	scratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
)

type artifactSchemaRegistry struct {
	*stubRegistry
	schema map[string]interface{}
}

func (r artifactSchemaRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) {
	return &llm.ToolDefinition{Parameters: r.schema}, true
}
func TestArtifactDispatchClonesNestedBinaryArgumentsAndPreservesMacro(t *testing.T) {
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owned-user"})
	id := "2f1c171e-0fe9-4aa7-8f17-5eb826f72042"
	data := []byte{0, 1, 255, 13, 10}
	_, err := scratchpad.New().PublishArtifact(ctx, id, "owned.bin", "application/octet-stream", "", bytes.NewReader(data))
	require.NoError(t, err)
	binary := map[string]interface{}{"type": "string", "contentEncoding": "base64"}
	schema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"uploads": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"body": binary}}}}}
	macro := "${artifact[" + id + "].payload}"
	args := map[string]interface{}{"uploads": []interface{}{map[string]interface{}{"body": macro}}}
	registry := artifactSchemaRegistry{stubRegistry: &stubRegistry{}, schema: schema}
	execution, payloads, err := artifactDispatchArguments(ctx, registry, "owned/upload", args)
	require.NoError(t, err)
	require.Equal(t, macro, args["uploads"].([]interface{})[0].(map[string]interface{})["body"])
	encoded := base64.StdEncoding.EncodeToString(data)
	require.Equal(t, encoded, execution["uploads"].([]interface{})[0].(map[string]interface{})["body"])
	require.NotContains(t, redactDispatchArtifactOutput("echo "+encoded, payloads), encoded)
	binary["contentEncoding"] = "unsupported"
	_, _, err = artifactDispatchArguments(ctx, registry, "owned/upload", args)
	require.ErrorContains(t, err, "encoding")
	_, _, err = artifactDispatchArguments(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "foreign"}), registry, "owned/upload", args)
	require.Error(t, err)
}

func TestArtifactDispatchArgumentBoundsAndCancellation(t *testing.T) {
	nested := map[string]interface{}{}
	root := nested
	for i := 0; i < 34; i++ {
		child := map[string]interface{}{}
		nested["child"] = child
		nested = child
	}
	require.ErrorContains(t, validateArtifactArgumentBudget(context.Background(), root), "structure")
	require.ErrorContains(t, validateArtifactArgumentBudget(context.Background(), map[string]interface{}{"large": strings.Repeat("x", (1<<20)+1)}), "size")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, validateArtifactArgumentBudget(ctx, root), context.Canceled)
}

type artifactExecutingRegistry struct {
	artifactSchemaRegistry
	t        *testing.T
	received []byte
}

func (r *artifactExecutingRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	execution, _, err := tool.ResolveDispatchPayload(ctx, name, args)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(execution)
	if err != nil {
		return "", err
	}
	var input struct {
		Body []byte `json:"body"`
	}
	if err = json.Unmarshal(raw, &input); err != nil {
		return "", err
	}
	r.received = append([]byte(nil), input.Body...)
	return `{"uploaded":true}`, nil
}
func TestArtifactToolStepPersistsOnlyMacroAndMetadata(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "owned-tool-trace.jsonl")
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", tracePath)
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx := authctx.WithUserInfo(memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "owned-macro-conversation", TurnID: "owned-macro-turn", ParentMessageID: "owned-parent"}), &authctx.UserInfo{Subject: "owned-user"})
	data := []byte("site\nowned.example\n")
	id := "2f1c171e-0fe9-4aa7-8f17-5eb826f72042"
	_, err := scratchpad.New().PublishArtifact(ctx, id, "sites.csv", "text/csv", "", bytes.NewReader(data))
	require.NoError(t, err)
	macro := "${artifact[" + id + "].payload;xform=base64}"
	args := map[string]interface{}{"body": macro}
	registry := &artifactExecutingRegistry{artifactSchemaRegistry: artifactSchemaRegistry{stubRegistry: &stubRegistry{}, schema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"body": map[string]interface{}{"type": "string"}}}}}
	conv := &stubConv{}
	call, _, err := ExecuteToolStep(ctx, registry, StepInfo{ID: "owned-call", Name: "owned/upload", Args: args}, conv)
	require.NoError(t, err)
	require.Equal(t, data, registry.received)
	require.Equal(t, macro, call.Arguments["body"])
	require.Equal(t, macro, args["body"])
	persisted, err := json.Marshal(map[string]interface{}{"payloads": conv.patchedPayloads, "calls": conv.patchedToolCalls, "messages": conv.patchedMessages})
	require.NoError(t, err)
	requestSeen := false
	for _, payload := range conv.patchedPayloads {
		if payload.Kind == "tool_request" {
			require.Contains(t, string(*payload.InlineBody), macro)
			require.NotContains(t, string(*payload.InlineBody), base64.StdEncoding.EncodeToString(data))
			requestSeen = true
		}
	}
	require.True(t, requestSeen)
	trace, traceErr := os.ReadFile(tracePath)
	require.NoError(t, traceErr)
	require.Contains(t, string(trace), macro)
	require.NotContains(t, string(trace), "owned.example")
	require.NotContains(t, string(trace), base64.StdEncoding.EncodeToString(data))
	require.NotContains(t, string(persisted), base64.StdEncoding.EncodeToString(data))
	require.NotContains(t, string(persisted), "owned.example")
}

func TestArtifactMacroOccurrenceAndAggregateEncodedLimits(t *testing.T) {
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owned-user"})
	id := "2f1c171e-0fe9-4aa7-8f17-5eb826f72042"
	macro := "${artifact[" + id + "].payload}"
	_, err := scratchpad.New().PublishArtifact(ctx, id, "sites.csv", "text/csv", "", strings.NewReader("owned bytes"))
	require.NoError(t, err)
	schema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"uploads": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string", "contentEncoding": "base64"}}}}
	reg := artifactSchemaRegistry{stubRegistry: &stubRegistry{}, schema: schema}
	values := make([]interface{}, 33)
	for i := range values {
		values[i] = macro
	}
	_, _, err = artifactDispatchArguments(ctx, reg, "owned/upload", map[string]interface{}{"uploads": values})
	require.ErrorContains(t, err, "occurrence")
	largeID := "b1eb5fcb-fb2b-4e1a-b3c3-c6cba9de9c6a"
	_, err = scratchpad.New().PublishArtifact(ctx, largeID, "owned.bin", "application/octet-stream", "", bytes.NewReader(make([]byte, 7<<20)))
	require.NoError(t, err)
	largeMacro := "${artifact[" + largeID + "].payload}"
	_, _, err = artifactDispatchArguments(ctx, reg, "owned/upload", map[string]interface{}{"uploads": []interface{}{largeMacro, largeMacro}})
	require.ErrorContains(t, err, "aggregate")
}

func TestArtifactMacroReviewQueuesOriginalWithoutReadingMissingPayload(t *testing.T) {
	macro := "${artifact[2f1c171e-0fe9-4aa7-8f17-5eb826f72042].payload}"
	ctx := toolapprovalqueue.WithState(memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "owned-review", TurnID: "owned-review-turn", ParentMessageID: "owned-parent"}))
	toolapprovalqueue.MarkTool(ctx, "owned/upload", &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue})
	conv := &stubQueueConv{}
	reg := &artifactExecutingRegistry{artifactSchemaRegistry: artifactSchemaRegistry{stubRegistry: &stubRegistry{}, schema: map[string]interface{}{}}}
	_, _, err := ExecuteToolStep(ctx, reg, StepInfo{ID: "owned-review-call", Name: "owned/upload", Args: map[string]interface{}{"body": macro}}, conv)
	require.NoError(t, err)
	require.Empty(t, reg.received)
	require.NotNil(t, conv.rec)
	raw, marshalErr := json.Marshal(conv.rec)
	require.NoError(t, marshalErr)
	// Queue arguments are stored as bytes and JSON encodes that column as base64.
	require.Contains(t, string(raw), base64.StdEncoding.EncodeToString([]byte(`{"body":"`+macro+`"}`)))
}

func TestArtifactBinaryEchoFormsAndDeclaredPartialOutputAreWithheld(t *testing.T) {
	data := []byte{0, 255, 128, 13, 10, 34, 92}
	encoded := base64.StdEncoding.EncodeToString(data)
	payload := []dispatchArtifactPayload{{raw: string(data), encoded: encoded}}
	vector := make([]int, len(data))
	for i, b := range data {
		vector[i] = int(b)
	}
	numeric, _ := json.Marshal(vector)
	quoted := strconv.Quote(string(data))
	unicode := strings.ReplaceAll(encoded, "A", `\u0041`)
	for _, echo := range []string{string(data), encoded, fmt.Sprintf("%v", data), quoted, string(numeric), `{"message":"` + unicode + `"}`} {
		require.Equal(t, "[artifact payload echo withheld]", redactDispatchArtifactOutput(echo, payload))
	}
	require.Equal(t, `{"accepted":true,"count":7}`, redactDispatchArtifactOutput(`{"accepted":true,"count":7}`, payload))
	shape := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"body": map[string]interface{}{"type": "string", "contentEncoding": "base64"}}}
	require.Equal(t, "[binary tool output withheld]", redactDeclaredBinaryOutput(`{"structuredContent":{"body":"AP8="}}`, shape))
	require.Equal(t, `{"accepted":true}`, redactDeclaredBinaryOutput(`{"accepted":true}`, shape))
	require.Equal(t, `{"accepted":true}`, redactDispatchArtifactOutput(`{"accepted":true}`, []dispatchArtifactPayload{{}}))
}

func TestArtifactResolverLeavesLargeLegacyNonmacroCallsUnchanged(t *testing.T) {
	value := strings.Repeat("A", (1<<20)+1024)
	args := map[string]interface{}{"body": value}
	execution, payloads, err := artifactDispatchArguments(context.Background(), artifactSchemaRegistry{}, "legacy/upload", args)
	require.NoError(t, err)
	require.Empty(t, payloads)
	require.Equal(t, value, execution["body"])
	execution["same-map"] = true
	require.Equal(t, true, args["same-map"], "legacy path does not clone or impose artifact bounds")
}

type artifactAsyncRegistry struct {
	artifactSchemaRegistry
	cfg      *asynccfg.Config
	pollDone chan struct{}
	runs     atomic.Int32
	statuses atomic.Int32
}

func (r *artifactAsyncRegistry) AsyncConfig(name string) (*asynccfg.Config, bool) {
	return r.cfg, name == r.cfg.Run.Tool || name == r.cfg.Status.Tool
}
func (r *artifactAsyncRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	execution, sanitize, err := tool.ResolveDispatchPayload(ctx, name, args)
	if err != nil {
		return "", err
	}
	if name == r.cfg.Run.Tool {
		encoded, _ := execution["body"].(string)
		if _, err = base64.StdEncoding.DecodeString(encoded); err != nil {
			return "", err
		}
		r.runs.Add(1)
		return sanitize(`{"status":"running","operationId":"owned-business-id"}`), nil
	}
	if execution["operationId"] != "owned-business-id" {
		return "", fmt.Errorf("business operation identity changed")
	}
	r.statuses.Add(1)
	select {
	case <-r.pollDone:
	default:
		close(r.pollDone)
	}
	return sanitize(`{"status":"completed","count":2}`), nil
}
func TestArtifactMacroAsyncAcceptedControlPollKeepsOriginalIdentity(t *testing.T) {
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx, cancel := context.WithTimeout(authctx.WithUserInfo(memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "owned-async-chat", TurnID: "owned-async-turn", ParentMessageID: "owned-parent"}), &authctx.UserInfo{Subject: "owned-user"}), 3*time.Second)
	defer cancel()
	id := "2f1c171e-0fe9-4aa7-8f17-5eb826f72042"
	_, err := scratchpad.New().PublishArtifact(ctx, id, "sites.csv", "text/csv", "", strings.NewReader("site\nowned.example\n"))
	require.NoError(t, err)
	macro := "${artifact[" + id + "].payload}"
	cfg := &asynccfg.Config{DefaultExecutionMode: string(asynccfg.ExecutionModeWait), PollIntervalMs: 5, Run: asynccfg.RunConfig{Tool: "owned/start", OperationIDPath: "operationId", Selector: &asynccfg.Selector{StatusPath: "status"}}, Status: asynccfg.StatusConfig{Tool: "owned/status", OperationIDArg: "operationId", Selector: asynccfg.Selector{StatusPath: "status"}}}
	registry := &artifactAsyncRegistry{artifactSchemaRegistry: artifactSchemaRegistry{stubRegistry: &stubRegistry{}, schema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"body": map[string]interface{}{"type": "string", "contentEncoding": "base64"}}}}, cfg: cfg, pollDone: make(chan struct{})}
	ctx = WithAsyncManager(ctx, asynccfg.NewManager())
	conv := &stubConv{}
	args := map[string]interface{}{"body": macro}
	call, _, err := ExecuteToolStep(ctx, registry, StepInfo{ID: "owned-async-op", Name: "owned/start", Args: args}, conv)
	require.NoError(t, err)
	require.Equal(t, macro, call.Arguments["body"])
	select {
	case <-registry.pollDone:
	case <-ctx.Done():
		t.Fatal("owned accepted operation did not poll")
	}
	require.EqualValues(t, 1, registry.runs.Load())
	require.Positive(t, registry.statuses.Load())
	require.Equal(t, macro, args["body"])
}

func TestArtifactAsyncRehydratedStatusFailsClosedWithoutOriginalSession(t *testing.T) {
	macro := "${artifact[2f1c171e-0fe9-4aa7-8f17-5eb826f72042].payload}"
	cfg := &asynccfg.Config{Run: asynccfg.RunConfig{Tool: "owned/start"}, Status: asynccfg.StatusConfig{Tool: "owned/status", OperationIDArg: "operationId", Selector: asynccfg.Selector{StatusPath: "status"}}}
	for _, actor := range []string{"owner", "foreign"} {
		t.Run(actor, func(t *testing.T) {
			owner := authctx.WithUserInfo(memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "owned-replay-chat", TurnID: "owned-replay-turn"}), &authctx.UserInfo{Subject: "owner"})
			manager := asynccfg.NewManager()
			manager.Register(owner, asynccfg.RegisterInput{ID: "owned-business-id", ParentConvID: "owned-replay-chat", ToolName: "owned/start", StatusToolName: "owned/status", RequestArgs: map[string]interface{}{"body": macro}, Status: "running"})
			ctx := WithAsyncManager(authctx.WithUserInfo(owner, &authctx.UserInfo{Subject: actor}), manager)
			reg := &artifactAsyncRegistry{artifactSchemaRegistry: artifactSchemaRegistry{stubRegistry: &stubRegistry{}, schema: map[string]interface{}{}}, cfg: cfg, pollDone: make(chan struct{})}
			_, _, err := executeTool(ctx, reg, StepInfo{ID: "replay-status", Name: "owned/status", Args: map[string]interface{}{"operationId": "owned-business-id"}}, &stubConv{})
			require.Error(t, err)
			if actor == "owner" {
				require.ErrorContains(t, err, "original owned dispatch context")
			} else {
				require.ErrorContains(t, err, "owner mismatch")
			}
			require.Zero(t, reg.statuses.Load())
			require.Zero(t, reg.runs.Load())
		})
	}
}

func TestArtifactExplicitBase64OnUndeclaredStringContract(t *testing.T) {
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owned-user"})
	id := "2f1c171e-0fe9-4aa7-8f17-5eb826f72042"
	bytesValue := []byte{0, 255, 128, 34, 10}
	_, err := scratchpad.New().PublishArtifact(ctx, id, "owned.bin", "application/octet-stream", "", bytes.NewReader(bytesValue))
	require.NoError(t, err)
	plain := "${artifact[" + id + "].payload}"
	explicit := "${artifact[" + id + "].payload;xform=base64}"
	field := map[string]interface{}{"type": "string"}
	schema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"Files": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"data": field}}}}}
	reg := artifactSchemaRegistry{stubRegistry: &stubRegistry{}, schema: schema}
	args := map[string]interface{}{"Files": []interface{}{map[string]interface{}{"data": explicit}}}
	execution, _, err := artifactDispatchArguments(ctx, reg, "generic/upload", args)
	require.NoError(t, err)
	require.Equal(t, base64.StdEncoding.EncodeToString(bytesValue), execution["Files"].([]interface{})[0].(map[string]interface{})["data"])
	require.Equal(t, explicit, args["Files"].([]interface{})[0].(map[string]interface{})["data"])
	args["Files"].([]interface{})[0].(map[string]interface{})["data"] = plain
	_, _, err = artifactDispatchArguments(ctx, reg, "generic/upload", args)
	require.ErrorContains(t, err, "encoding")
	args["Files"].([]interface{})[0].(map[string]interface{})["data"] = explicit
	for _, format := range []string{"hex", "date", "date-time", "email", "binary"} {
		field["format"] = format
		_, _, err = artifactDispatchArguments(ctx, reg, "generic/upload", args)
		require.ErrorContains(t, err, "encoding")
	}
	delete(field, "format")
	field["contentEncoding"] = "hex"
	_, _, err = artifactDispatchArguments(ctx, reg, "generic/upload", args)
	require.ErrorContains(t, err, "encoding")
	delete(field, "contentEncoding")
	field["type"] = "integer"
	_, _, err = artifactDispatchArguments(ctx, reg, "generic/upload", args)
	require.ErrorContains(t, err, "string")
	field["type"] = "string"
	args["Files"].([]interface{})[0].(map[string]interface{})["data"] = "prefix " + explicit
	_, _, err = artifactDispatchArguments(ctx, reg, "generic/upload", args)
	require.ErrorContains(t, err, "exact UUID")
}

type artifactStatusOutputRegistry struct {
	artifactSchemaRegistry
	statusOutput string
}

func (r *artifactStatusOutputRegistry) GetDefinition(name string) (*llm.ToolDefinition, bool) {
	definition, _ := r.artifactSchemaRegistry.GetDefinition(name)
	if name == "owned/status" {
		definition.OutputSchema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{"preview": map[string]interface{}{"type": "string", "contentEncoding": "base64"}}}
	}
	return definition, true
}

func (r *artifactStatusOutputRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	_, _, err := tool.ResolveDispatchPayload(ctx, name, args)
	if err != nil {
		return "", err
	}
	statusArgs := map[string]interface{}{"operationId": "business-operation"}
	execution, sanitize, err := tool.ResolveDispatchPayload(ctx, "owned/status", statusArgs)
	if err != nil {
		return "", err
	}
	if execution["operationId"] != statusArgs["operationId"] {
		return "", fmt.Errorf("operation identity changed")
	}
	r.statusOutput = sanitize(`{"structuredContent":{"preview":"AQID"}}`)
	return `{"accepted":true}`, nil
}

func TestArtifactAsyncStatusUsesCurrentOutputSchema(t *testing.T) {
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx := authctx.WithUserInfo(memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "owned-chat", TurnID: "owned-turn", ParentMessageID: "owned-parent"}), &authctx.UserInfo{Subject: "owned-user"})
	id := "2f1c171e-0fe9-4aa7-8f17-5eb826f72042"
	_, err := scratchpad.New().PublishArtifact(ctx, id, "owned.bin", "application/octet-stream", "", strings.NewReader("original artifact bytes"))
	require.NoError(t, err)
	reg := &artifactStatusOutputRegistry{artifactSchemaRegistry: artifactSchemaRegistry{stubRegistry: &stubRegistry{}, schema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"body": map[string]interface{}{"type": "string", "contentEncoding": "base64"}}}}}
	macro := "${artifact[" + id + "].payload}"
	call, _, err := ExecuteToolStep(ctx, reg, StepInfo{ID: "status-output", Name: "owned/start", Args: map[string]interface{}{"body": macro}}, &stubConv{})
	require.NoError(t, err)
	require.Equal(t, "[binary tool output withheld]", reg.statusOutput)
	require.Equal(t, macro, call.Arguments["body"])
}
