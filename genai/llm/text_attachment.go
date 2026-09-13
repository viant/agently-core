package llm

import (
	"bytes"
	"fmt"
	"mime"
	"strings"
	"unicode/utf8"
)

// TextAttachmentContent preserves textual uploads for text-only models instead
// of passing CSV/JSON as binary content that multimodal policy must discard.
func TextAttachmentContent(data []byte, mimeType, name string) (ContentItem, bool) {
	mediaType, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return ContentItem{}, false
	}
	mediaType = strings.ToLower(mediaType)
	isText := strings.HasPrefix(mediaType, "text/")
	switch mediaType {
	case "application/json", "application/xml", "application/yaml", "application/x-yaml", "application/csv":
		isText = true
	}
	if !isText || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return ContentItem{}, false
	}
	if strings.TrimSpace(name) == "" {
		name = "attachment"
	}
	body := strings.TrimPrefix(string(data), "\ufeff")
	return NewTextContent(fmt.Sprintf("Text attachment %q (%s):\n%s", name, mediaType, body)), true
}
