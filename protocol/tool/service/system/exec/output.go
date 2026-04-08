package exec

import "github.com/viant/mcp-protocol/extension"

// Command represents the result of executing a single command
type Command struct {
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Status int    `json:"status,omitempty"`
}

// Output represents the results of executing commands
type Output struct {
	SessionID    string                  `json:"sessionId,omitempty"`
	Commands     []*Command              `json:"commands,omitempty"`
	Stream       string                  `json:"stream,omitempty"`
	Stdout       string                  `json:"stdout,omitempty" description:"Full buffered stdout accumulated so far. Not truncated."`
	Stderr       string                  `json:"stderr,omitempty" description:"Full buffered stderr accumulated so far. Not truncated."`
	Content      string                  `json:"content,omitempty" description:"Selected stream content slice when stream/byteRange paging is used."`
	Offset       int                     `json:"offset,omitempty"`
	Limit        int                     `json:"limit,omitempty"`
	Size         int                     `json:"size,omitempty"`
	Continuation *extension.Continuation `json:"continuation,omitempty" description:"Native byte-range continuation for the selected stream content."`
	Status       int                     `json:"status,omitempty"`
}
