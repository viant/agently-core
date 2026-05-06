package anthropic

import vclaude "github.com/viant/agently-core/genai/llm/provider/vertexai/claude"

type Message = vclaude.Message
type ContentBlock = vclaude.ContentBlock
type ToolDefinition = vclaude.ToolDefinition
type Source = vclaude.Source
type Thinking = vclaude.Thinking
type Response = vclaude.Response
type Usage = vclaude.Usage

// Request represents the direct Anthropic Messages API payload.
type Request struct {
	Model         string           `json:"model"`
	Messages      []Message        `json:"messages"`
	Tools         []ToolDefinition `json:"tools,omitempty"`
	MaxTokens     int              `json:"max_tokens,omitempty"`
	Temperature   float64          `json:"temperature,omitempty"`
	TopP          float64          `json:"top_p,omitempty"`
	TopK          int              `json:"top_k,omitempty"`
	StopSequences []string         `json:"stop_sequences,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
	Thinking      *Thinking        `json:"thinking,omitempty"`
	System        string           `json:"system,omitempty"`
}
