package gemini

import (
	"fmt"
	"github.com/viant/agently-core/genai/llm"
)

func protocolContent(item llm.ContentItem) (Part, bool, error) {
	if item.Source != llm.SourceFile && (item.Metadata == nil || item.Metadata["ag-ui.contentPart"] != true) {
		return Part{}, false, nil
	}
	if item.Type == llm.ContentTypeText {
		return Part{Text: item.Data}, true, nil
	}
	if item.Source == llm.SourceFile && item.Provider != "" && item.Provider != "google" {
		return Part{}, true, fmt.Errorf("Gemini cannot resolve %s provider file handle", item.Provider)
	}
	if item.MimeType == "" {
		return Part{}, true, fmt.Errorf("Gemini media content requires mimeType")
	}
	switch item.Source {
	case llm.SourceFile, llm.SourceURL:
		return Part{FileData: &FileData{MimeType: item.MimeType, FileURI: item.Data}}, true, nil
	case llm.SourceBase64:
		return Part{InlineData: &InlineData{MimeType: item.MimeType, Data: item.Data}}, true, nil
	default:
		return Part{}, true, fmt.Errorf("Gemini unsupported media source: %s", item.Source)
	}
}
