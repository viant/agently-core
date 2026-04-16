package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	promptdef "github.com/viant/agently-core/protocol/prompt"
)

// --- mock model finder + model ---

type mockModel struct {
	response string
	err      error
}

func (m *mockModel) Generate(_ context.Context, _ *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.GenerateResponse{
		Choices: []llm.Choice{{Message: llm.Message{Content: m.response}}},
	}, nil
}

func (m *mockModel) Implements(_ string) bool { return false }

type mockModelFinder struct {
	model llm.Model
	err   error
}

func (f *mockModelFinder) Find(_ context.Context, _ string) (llm.Model, error) {
	return f.model, f.err
}

func svcWithFinder(f llm.Finder) *Service {
	return &Service{modelFinder: f}
}

// --- tests ---

func TestExpandMessages_NoFinderReturnsOriginal(t *testing.T) {
	s := &Service{} // no modelFinder
	msgs := []promptdef.Message{{Role: "system", Text: "original"}}
	cfg := &promptdef.Expansion{Mode: "llm", Model: "haiku"}
	result := s.expandMessages(context.Background(), msgs, "objective", cfg)
	assert.Equal(t, msgs, result)
}

func TestExpandMessages_EmptyCfgReturnsOriginal(t *testing.T) {
	s := svcWithFinder(&mockModelFinder{model: &mockModel{}})
	msgs := []promptdef.Message{{Role: "system", Text: "original"}}
	result := s.expandMessages(context.Background(), msgs, "objective", nil)
	assert.Equal(t, msgs, result)
}

func TestExpandMessages_EmptyModelReturnsOriginal(t *testing.T) {
	s := svcWithFinder(&mockModelFinder{model: &mockModel{}})
	msgs := []promptdef.Message{{Role: "system", Text: "original"}}
	cfg := &promptdef.Expansion{Mode: "llm", Model: ""}
	result := s.expandMessages(context.Background(), msgs, "objective", cfg)
	assert.Equal(t, msgs, result)
}

func TestExpandMessages_Success(t *testing.T) {
	refined := []promptdef.Message{
		{Role: "system", Text: "You are analyzing campaign 4821 underpacing."},
		{Role: "user", Text: "Focus on week-over-week pacing for campaign 4821."},
	}
	raw, _ := json.Marshal(refined)
	s := svcWithFinder(&mockModelFinder{model: &mockModel{response: string(raw)}})
	original := []promptdef.Message{
		{Role: "system", Text: "You are a performance analyst."},
		{Role: "user", Text: "Analyze the campaign hierarchy."},
	}
	cfg := &promptdef.Expansion{Mode: "llm", Model: "haiku", MaxTokens: 600}
	result := s.expandMessages(context.Background(), original, "Why is campaign 4821 underpacing?", cfg)
	require.Len(t, result, 2)
	assert.Equal(t, "system", result[0].Role)
	assert.Contains(t, result[0].Text, "4821")
}

func TestExpandMessages_LLMErrorFallsBack(t *testing.T) {
	networkErr := fmt.Errorf("network error")
	s := svcWithFinder(&mockModelFinder{err: networkErr})
	msgs := []promptdef.Message{{Role: "system", Text: "original"}}
	cfg := &promptdef.Expansion{Mode: "llm", Model: "haiku"}
	result := s.expandMessages(context.Background(), msgs, "objective", cfg)
	assert.Equal(t, msgs, result, "should fall back to original on LLM error")
}

func TestExpandMessages_RoleStructureMismatchFallsBack(t *testing.T) {
	// Sidecar returns wrong number of messages
	refined := []promptdef.Message{{Role: "system", Text: "only one"}}
	raw, _ := json.Marshal(refined)
	s := svcWithFinder(&mockModelFinder{model: &mockModel{response: string(raw)}})
	original := []promptdef.Message{
		{Role: "system", Text: "sys"},
		{Role: "user", Text: "usr"},
	}
	cfg := &promptdef.Expansion{Mode: "llm", Model: "haiku"}
	result := s.expandMessages(context.Background(), original, "obj", cfg)
	assert.Equal(t, original, result, "role count mismatch should fall back")
}

func TestExpandMessages_RoleNameMismatchFallsBack(t *testing.T) {
	// Sidecar flips system → user
	refined := []promptdef.Message{
		{Role: "user", Text: "changed role"},
		{Role: "system", Text: "changed role 2"},
	}
	raw, _ := json.Marshal(refined)
	s := svcWithFinder(&mockModelFinder{model: &mockModel{response: string(raw)}})
	original := []promptdef.Message{
		{Role: "system", Text: "sys"},
		{Role: "user", Text: "usr"},
	}
	cfg := &promptdef.Expansion{Mode: "llm", Model: "haiku"}
	result := s.expandMessages(context.Background(), original, "obj", cfg)
	assert.Equal(t, original, result, "role name mismatch should fall back")
}

func TestExpandMessages_EmptyTextFallsBack(t *testing.T) {
	refined := []promptdef.Message{
		{Role: "system", Text: ""},
	}
	raw, _ := json.Marshal(refined)
	s := svcWithFinder(&mockModelFinder{model: &mockModel{response: string(raw)}})
	original := []promptdef.Message{{Role: "system", Text: "original"}}
	cfg := &promptdef.Expansion{Mode: "llm", Model: "haiku"}
	result := s.expandMessages(context.Background(), original, "obj", cfg)
	assert.Equal(t, original, result, "empty text should fall back")
}

func TestParseExpansionOutput_PlainJSON(t *testing.T) {
	input := `[{"role":"system","text":"hello"},{"role":"user","text":"world"}]`
	out, err := parseExpansionOutput(input)
	require.NoError(t, err)
	assert.Len(t, out, 2)
}

func TestParseExpansionOutput_FencedMarkdown(t *testing.T) {
	input := "```json\n[{\"role\":\"system\",\"text\":\"hello\"}]\n```"
	out, err := parseExpansionOutput(input)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "system", out[0].Role)
}

func TestParseExpansionOutput_LeadingProse(t *testing.T) {
	// Sidecar adds preamble prose before the JSON array
	input := "Here are the refined instructions:\n[{\"role\":\"system\",\"text\":\"ok\"}]"
	out, err := parseExpansionOutput(input)
	require.NoError(t, err)
	require.Len(t, out, 1)
}

func TestStripMarkdownFence(t *testing.T) {
	cases := []struct{ in, want string }{
		{"```json\n[]\n```", "[]"},
		{"```\n[]\n```", "[]"},
		{"[]", "[]"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, stripMarkdownFence(c.in))
	}
}
