package openai

import (
	"fmt"
	"github.com/viant/agently-core/genai/llm"
	"strings"
)

// protocolContent maps protocol media without fetching URLs or uploading opaque
// provider handles. Unsupported model/API combinations fail before HTTP dispatch.
func (c *Client) protocolContent(item llm.ContentItem) (ContentItem, bool, error) {
	if item.Source == llm.SourceFile {
		if item.Type == llm.ContentTypeAudio || item.Type == llm.ContentTypeVideo {
			return ContentItem{}, true, fmt.Errorf("OpenAI does not support %s provider file parts", item.Type)
		}
		if item.Provider != "" && item.Provider != "openai" {
			return ContentItem{}, true, fmt.Errorf("OpenAI cannot resolve %s provider file handle", item.Provider)
		}
		kind := "file"
		if item.Type == llm.ContentTypeImage {
			if !supportsUploadedImageResponses(c) {
				return ContentItem{}, true, fmt.Errorf("OpenAI image file handles require a Responses-capable endpoint")
			}
			kind = "image_file"
		}
		return ContentItem{Type: kind, File: &File{FileID: item.Data}}, true, nil
	}
	switch item.Type {
	case llm.ContentTypeImage:
		if item.Source == llm.SourceBase64 {
			return ContentItem{Type: "image_url", ImageURL: &ImageURL{URL: dataURLForBase64(item.MimeType, item.Data)}}, true, nil
		}
	case llm.ContentTypePDF:
		if item.Source == llm.SourceURL {
			if !isContextContinuationEnabled(c) {
				return ContentItem{}, true, fmt.Errorf("OpenAI document URL sources require Responses API")
			}
			return ContentItem{Type: "file", File: &File{FileURL: item.Data}}, true, nil
		}
		if item.Source == llm.SourceBase64 {
			return ContentItem{Type: "file", File: &File{FileName: defaultInputFileName(item.Name, item.MimeType), FileData: dataURLForBase64(item.MimeType, item.Data)}}, true, nil
		}
	case llm.ContentTypeBinary:
		if item.Source == llm.SourceBase64 && item.Metadata["ag-ui.contentPart"] == true {
			if !isOpenAIInputFileSupported(item.MimeType, item.Name) {
				return ContentItem{}, true, fmt.Errorf("OpenAI does not support inline document MIME type %q", item.MimeType)
			}
			return ContentItem{Type: "file", File: &File{FileName: defaultInputFileName(item.Name, item.MimeType), FileData: dataURLForBase64(item.MimeType, item.Data)}}, true, nil
		}
		if item.Source == llm.SourceURL {
			if !isContextContinuationEnabled(c) {
				return ContentItem{}, true, fmt.Errorf("OpenAI document URL sources require Responses API")
			}
			return ContentItem{Type: "file", File: &File{FileURL: item.Data}}, true, nil
		}
	case llm.ContentTypeAudio:
		if isContextContinuationEnabled(c) {
			return ContentItem{}, true, fmt.Errorf("OpenAI Responses content mapping does not support audio parts")
		}
		format := ""
		switch strings.ToLower(item.MimeType) {
		case "audio/wav", "audio/x-wav":
			format = "wav"
		case "audio/mpeg", "audio/mp3":
			format = "mp3"
		}
		if item.Source != llm.SourceBase64 || format == "" {
			return ContentItem{}, true, fmt.Errorf("OpenAI audio parts require inline WAV or MP3 data")
		}
		return ContentItem{Type: "input_audio", InputAudio: &InputAudio{Data: item.Data, Format: format}}, true, nil
	case llm.ContentTypeVideo:
		return ContentItem{}, true, fmt.Errorf("OpenAI does not support video content parts")
	}
	return ContentItem{}, false, nil
}
