package codec

import authored "github.com/viant/forge/backend/authoring/markdown"

// MarkdownMessage and MarkdownDocument retain Core's local loader API while
// Forge supplies the shared grammar used by AIStudio and other producers.
type MarkdownMessage = authored.Message
type MarkdownDocument = authored.Document

func ParseMarkdown(raw []byte) (MarkdownDocument, error) { return authored.Parse(raw) }
func MarshalMarkdown(metadata []byte, messages []MarkdownMessage) ([]byte, error) {
	return authored.Marshal(metadata, messages)
}
