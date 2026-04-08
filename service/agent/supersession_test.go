package agent

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/protocol/tool"
)

// stubCacheableRegistry implements tool.Registry for testing cacheable lookups.
type stubCacheableRegistry struct {
	defs map[string]*llm.ToolDefinition
}

func (r *stubCacheableRegistry) Definitions() []llm.ToolDefinition            { return nil }
func (r *stubCacheableRegistry) MatchDefinition(string) []*llm.ToolDefinition { return nil }
func (r *stubCacheableRegistry) MustHaveTools([]string) ([]llm.Tool, error)   { return nil, nil }
func (r *stubCacheableRegistry) SetDebugLogger(io.Writer)                     {}
func (r *stubCacheableRegistry) Initialize(context.Context)                   {}
func (r *stubCacheableRegistry) Execute(_ context.Context, _ string, _ map[string]interface{}) (string, error) {
	return "", nil
}
func (r *stubCacheableRegistry) GetDefinition(name string) (*llm.ToolDefinition, bool) {
	if r.defs == nil {
		return nil, false
	}
	d, ok := r.defs[name]
	return d, ok
}

var _ tool.Registry = (*stubCacheableRegistry)(nil)

// makeTCMsg creates an apiconv.Message with tool call metadata.
// argsJSON is the raw JSON string for the tool call arguments.
func makeTCMsg(toolName string, argsJSON *string, content string) *apiconv.Message {
	tc := &agconv.ToolCallView{
		ToolName: toolName,
	}
	if argsJSON != nil {
		tc.RequestPayload = &agconv.ModelCallStreamPayloadView{
			InlineBody: argsJSON,
		}
	}
	return &apiconv.Message{
		Role:    "assistant",
		Content: &content,
		ToolMessage: []*agconv.ToolMessageView{{
			ToolCall: tc,
		}},
	}
}

func TestSupersessionKey_IdenticalArgsDifferentOrder(t *testing.T) {
	k1 := supersessionKey("resources/read", map[string]interface{}{"uri": "file.go", "encoding": "utf-8"})
	k2 := supersessionKey("resources/read", map[string]interface{}{"encoding": "utf-8", "uri": "file.go"})
	assert.Equal(t, k1, k2, "same args different order should produce same key")
}

func TestSupersessionKey_DifferentArgs(t *testing.T) {
	k1 := supersessionKey("resources/read", map[string]interface{}{"uri": "a.go"})
	k2 := supersessionKey("resources/read", map[string]interface{}{"uri": "b.go"})
	assert.NotEqual(t, k1, k2, "different args should produce different keys")
}

func TestSupersessionKey_DifferentToolNames(t *testing.T) {
	k1 := supersessionKey("resources/read", map[string]interface{}{"uri": "a.go"})
	k2 := supersessionKey("resources/list", map[string]interface{}{"uri": "a.go"})
	assert.NotEqual(t, k1, k2, "different tool names should produce different keys")
}

func TestSupersessionKey_EmptyArgs(t *testing.T) {
	k1 := supersessionKey("resources/list", nil)
	k2 := supersessionKey("resources/list", map[string]interface{}{})
	assert.Equal(t, k1, k2, "nil and empty args should produce same key")
}

func TestApplyToolCallSupersession_HistoryKeepsNewest(t *testing.T) {
	reg := &stubCacheableRegistry{defs: map[string]*llm.ToolDefinition{
		"resources/read": {Name: "resources/read", Cacheable: true},
	}}
	args := strPtr(`{"uri":"file.go"}`)
	msgs := []normalizedMsg{
		{turnIdx: 0, msg: makeTCMsg("resources/read", args, "old content")},
		{turnIdx: 1, msg: makeTCMsg("resources/read", args, "newer content")},
		{turnIdx: 2, msg: makeTCMsg("resources/read", args, "newest content")},
	}
	result := applyToolCallSupersession(msgs, 3, reg, &config.Compaction{})
	require.Len(t, result, 1, "should keep only newest across history")
	assert.Equal(t, "newest content", *result[0].msg.Content)
}

func TestApplyToolCallSupersession_NonCacheableNeverSuppressed(t *testing.T) {
	reg := &stubCacheableRegistry{defs: map[string]*llm.ToolDefinition{
		"system/exec/execute": {Name: "system/exec/execute", Cacheable: false},
	}}
	args := strPtr(`{"commands":["ls"]}`)
	msgs := []normalizedMsg{
		{turnIdx: 0, msg: makeTCMsg("system/exec/execute", args, "result1")},
		{turnIdx: 1, msg: makeTCMsg("system/exec/execute", args, "result2")},
		{turnIdx: 2, msg: makeTCMsg("system/exec/execute", args, "result3")},
	}
	result := applyToolCallSupersession(msgs, 3, reg, &config.Compaction{})
	require.Len(t, result, 3, "non-cacheable tools should never be suppressed")
}

func TestApplyToolCallSupersession_CurrentTurnKeepsLast2(t *testing.T) {
	reg := &stubCacheableRegistry{defs: map[string]*llm.ToolDefinition{
		"resources/read": {Name: "resources/read", Cacheable: true},
	}}
	args := strPtr(`{"uri":"file.go"}`)
	msgs := []normalizedMsg{
		{turnIdx: 0, msg: makeTCMsg("resources/read", args, "call1")},
		{turnIdx: 0, msg: makeTCMsg("resources/read", args, "call2")},
		{turnIdx: 0, msg: makeTCMsg("resources/read", args, "call3")},
		{turnIdx: 0, msg: makeTCMsg("resources/read", args, "call4")},
	}
	result := applyToolCallSupersession(msgs, 0, reg, &config.Compaction{})
	require.Len(t, result, 2, "current turn should keep last 2")
	assert.Equal(t, "call3", *result[0].msg.Content)
	assert.Equal(t, "call4", *result[1].msg.Content)
}

func TestApplyToolCallSupersession_DisabledLeavesAllIntact(t *testing.T) {
	reg := &stubCacheableRegistry{defs: map[string]*llm.ToolDefinition{
		"resources/read": {Name: "resources/read", Cacheable: true},
	}}
	args := strPtr(`{"uri":"file.go"}`)
	msgs := []normalizedMsg{
		{turnIdx: 0, msg: makeTCMsg("resources/read", args, "old")},
		{turnIdx: 1, msg: makeTCMsg("resources/read", args, "new")},
	}
	enabled := false
	result := applyToolCallSupersession(msgs, 2, reg, &config.Compaction{
		ToolCallSupersession: &config.ToolCallSupersession{Enabled: &enabled},
	})
	require.Len(t, result, 2, "disabled supersession should leave all intact")
}

func TestApplyToolCallSupersession_MixedCacheableAndNot(t *testing.T) {
	reg := &stubCacheableRegistry{defs: map[string]*llm.ToolDefinition{
		"resources/read":      {Name: "resources/read", Cacheable: true},
		"system/exec/execute": {Name: "system/exec/execute", Cacheable: false},
	}}
	readArgs := strPtr(`{"uri":"file.go"}`)
	execArgs := strPtr(`{"commands":["ls"]}`)
	msgs := []normalizedMsg{
		{turnIdx: 0, msg: makeTCMsg("resources/read", readArgs, "read-old")},
		{turnIdx: 0, msg: makeTCMsg("system/exec/execute", execArgs, "exec1")},
		{turnIdx: 1, msg: makeTCMsg("resources/read", readArgs, "read-new")},
		{turnIdx: 1, msg: makeTCMsg("system/exec/execute", execArgs, "exec2")},
	}
	result := applyToolCallSupersession(msgs, 2, reg, &config.Compaction{})
	require.Len(t, result, 3, "should suppress only cacheable duplicates")
	contents := make([]string, len(result))
	for i, r := range result {
		contents[i] = *r.msg.Content
	}
	assert.Contains(t, contents, "exec1")
	assert.Contains(t, contents, "exec2")
	assert.Contains(t, contents, "read-new")
}
