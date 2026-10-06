package clienttool

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
)

const ContentMIME = "application/vnd.ag-ui.content+json"
const ToolErrorMIME = "application/vnd.ag-ui.tool-error"
const ItemsMIME = "application/vnd.ag-ui.llm-items+json"

// MapContent preserves part order and source semantics without fetching URLs or
// resolving provider-owned file handles. Providers decide which assets they can
// consume; raw protocol metadata remains carried in each mapped item's metadata.
func MapContent(raw json.RawMessage) ([]llm.ContentItem, error) {
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, fmt.Errorf("AG-UI content cannot be null")
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []llm.ContentItem{llm.NewTextContent(text)}, nil
	}
	var parts []struct {
		Type   string  `json:"type"`
		ID     string  `json:"id"`
		Text   *string `json:"text"`
		Source *struct {
			Type     string `json:"type"`
			Value    string `json:"value"`
			MimeType string `json:"mimeType"`
			Provider string `json:"provider"`
		} `json:"source"`
		Metadata json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("AG-UI content must be text or an ordered part array: %w", err)
	}
	items := make([]llm.ContentItem, 0, len(parts))
	for _, part := range parts {
		item := llm.ContentItem{Name: part.ID}
		if len(part.Metadata) > 0 {
			var metadata interface{}
			decoder := json.NewDecoder(bytes.NewReader(part.Metadata))
			decoder.UseNumber()
			if err := decoder.Decode(&metadata); err != nil {
				return nil, err
			}
			if object, ok := metadata.(map[string]interface{}); ok {
				item.Metadata = object
			} else {
				item.Metadata = map[string]interface{}{"ag-ui.partMetadata": metadata}
			}
		}
		switch part.Type {
		case "text":
			if part.Text == nil {
				return nil, fmt.Errorf("text part requires text")
			}
			item.Type = llm.ContentTypeText
			item.Source = llm.SourceRaw
			item.Data = *part.Text
			item.Text = *part.Text
		case "image", "audio", "video", "document":
			if part.Source == nil {
				return nil, fmt.Errorf("%s part requires a source", part.Type)
			}
			item.Type = llm.ContentType(part.Type)
			if part.Type == "document" {
				item.Type = llm.ContentTypeBinary
				if strings.EqualFold(part.Source.MimeType, "application/pdf") {
					item.Type = llm.ContentTypePDF
				}
			}
			item.Data = part.Source.Value
			item.MimeType = part.Source.MimeType
			item.Provider = part.Source.Provider
			switch part.Source.Type {
			case "url":
				item.Source = llm.SourceURL
			case "data":
				if item.MimeType == "" {
					return nil, fmt.Errorf("inline data requires mimeType")
				}
				if _, err := base64.StdEncoding.DecodeString(item.Data); err != nil {
					return nil, fmt.Errorf("invalid inline media base64: %w", err)
				}
				item.Source = llm.SourceBase64
			case "file":
				item.Source = llm.SourceFile
			default:
				return nil, fmt.Errorf("unknown AG-UI part source: %s", part.Source.Type)
			}
		default:
			return nil, fmt.Errorf("unknown AG-UI content part: %s", part.Type)
		}
		if item.Metadata == nil {
			item.Metadata = map[string]interface{}{}
		}
		item.Metadata["ag-ui.contentPart"] = true
		items = append(items, item)
	}
	return items, nil
}
func ContentText(items []llm.ContentItem) string {
	var text strings.Builder
	for _, item := range items {
		if item.Type == llm.ContentTypeText {
			text.WriteString(item.Data)
		}
	}
	return text.String()
}
