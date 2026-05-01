package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
)

// TestParseClassifierResult_UnifiedSchema covers the workspace-intake
// classifier's unified action envelope:
//
//	{"action":"route","agentId":"X"}
//	{"action":"answer","text":"..."}
//	{"action":"clarify","question":"..."}
//
// plus the legacy bare {"agentId":"X"} schema (back-compat).
func TestParseClassifierResult_UnifiedSchema(t *testing.T) {
	mkResp := func(content string) *llm.GenerateResponse {
		return &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Content: content}}}}
	}

	t.Run("action=route returns AgentID", func(t *testing.T) {
		got := parseClassifierResult(mkResp(`{"action":"route","agentId":"steward"}`), "agentId")
		assert.NotNil(t, got)
		assert.Equal(t, ClassifierActionRoute, got.Action)
		assert.Equal(t, "steward", got.AgentID)
		assert.Empty(t, got.Answer)
		assert.Empty(t, got.Question)
	})

	t.Run("action=answer returns Answer", func(t *testing.T) {
		got := parseClassifierResult(mkResp(`{"action":"answer","text":"## Summary\nWorkspace can do X, Y, Z."}`), "agentId")
		assert.NotNil(t, got)
		assert.Equal(t, ClassifierActionAnswer, got.Action)
		assert.Equal(t, "## Summary\nWorkspace can do X, Y, Z.", got.Answer)
		assert.Empty(t, got.AgentID)
		assert.Empty(t, got.Question)
	})

	t.Run("action=clarify returns Question", func(t *testing.T) {
		got := parseClassifierResult(mkResp(`{"action":"clarify","question":"Which order should I forecast?"}`), "agentId")
		assert.NotNil(t, got)
		assert.Equal(t, ClassifierActionClarify, got.Action)
		assert.Equal(t, "Which order should I forecast?", got.Question)
		assert.Empty(t, got.AgentID)
		assert.Empty(t, got.Answer)
	})

	t.Run("legacy schema with bare agentId still parses as route", func(t *testing.T) {
		got := parseClassifierResult(mkResp(`{"agentId":"forecaster"}`), "agentId")
		assert.NotNil(t, got)
		assert.Equal(t, ClassifierActionRoute, got.Action)
		assert.Equal(t, "forecaster", got.AgentID)
	})

	t.Run("legacy schema with snake_case", func(t *testing.T) {
		got := parseClassifierResult(mkResp(`{"agent_id":"analyst"}`), "agent_id")
		assert.NotNil(t, got)
		assert.Equal(t, ClassifierActionRoute, got.Action)
		assert.Equal(t, "analyst", got.AgentID)
	})

	t.Run("action=answer with empty text returns nil", func(t *testing.T) {
		got := parseClassifierResult(mkResp(`{"action":"answer","text":""}`), "agentId")
		assert.Nil(t, got, "empty answer text must not produce a usable result")
	})

	t.Run("action=clarify with empty question returns nil", func(t *testing.T) {
		got := parseClassifierResult(mkResp(`{"action":"clarify","question":""}`), "agentId")
		assert.Nil(t, got)
	})

	t.Run("action=route with empty agentId returns nil", func(t *testing.T) {
		got := parseClassifierResult(mkResp(`{"action":"route","agentId":""}`), "agentId")
		assert.Nil(t, got)
	})

	t.Run("fenced JSON parses", func(t *testing.T) {
		got := parseClassifierResult(mkResp("```json\n{\"action\":\"route\",\"agentId\":\"steward\"}\n```"), "agentId")
		assert.NotNil(t, got)
		assert.Equal(t, ClassifierActionRoute, got.Action)
		assert.Equal(t, "steward", got.AgentID)
	})

	t.Run("nil response returns nil", func(t *testing.T) {
		assert.Nil(t, parseClassifierResult(nil, "agentId"))
	})

	t.Run("empty response returns nil", func(t *testing.T) {
		assert.Nil(t, parseClassifierResult(mkResp(""), "agentId"))
	})

	t.Run("unknown action falls back to legacy agentId field", func(t *testing.T) {
		// Defensive parsing: when the LLM emits a malformed `action` value
		// but provides a valid `agentId`, recover by treating it as a route.
		// Better than silently dropping a usable selection.
		got := parseClassifierResult(mkResp(`{"action":"reroute","agentId":"x"}`), "agentId")
		assert.NotNil(t, got)
		assert.Equal(t, ClassifierActionRoute, got.Action)
		assert.Equal(t, "x", got.AgentID)
	})

	t.Run("unknown action with no agentId returns nil", func(t *testing.T) {
		got := parseClassifierResult(mkResp(`{"action":"reroute"}`), "agentId")
		assert.Nil(t, got, "unknown action with no usable agentId is nil")
	})

	t.Run("non-JSON content falls back to first token as agent id", func(t *testing.T) {
		got := parseClassifierResult(mkResp("steward"), "agentId")
		assert.NotNil(t, got)
		assert.Equal(t, ClassifierActionRoute, got.Action)
		assert.Equal(t, "steward", got.AgentID)
	})

	t.Run("answer text with newlines preserved", func(t *testing.T) {
		multiline := `{"action":"answer","text":"line one\nline two\nline three"}`
		got := parseClassifierResult(mkResp(multiline), "agentId")
		assert.NotNil(t, got)
		assert.Equal(t, "line one\nline two\nline three", got.Answer)
	})
}
