package claude

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/viant/agently-core/genai/llm"
	"strings"
	"unicode/utf8"
)

type directAPIKey struct{}

// WithDirectAPI enables direct Anthropic URL and provider-owned file sources;
// Vertex-hosted Claude does not share the direct Anthropic file namespace.
func WithDirectAPI(ctx context.Context) context.Context {
	return context.WithValue(ctx, directAPIKey{}, true)
}
func protocolContent(ctx context.Context, item llm.ContentItem) (ContentBlock, bool, error) {
	if item.Source != llm.SourceFile && (item.Metadata == nil || item.Metadata["ag-ui.contentPart"] != true) {
		return ContentBlock{}, false, nil
	}
	if item.Type == llm.ContentTypeText {
		return ContentBlock{Type: "text", Text: item.Data}, true, nil
	}
	kind := ""
	switch item.Type {
	case llm.ContentTypeImage:
		kind = "image"
	case llm.ContentTypePDF, llm.ContentTypeBinary:
		kind = "document"
	default:
		return ContentBlock{}, true, fmt.Errorf("Claude does not support %s content parts", item.Type)
	}
	source := &Source{}
	switch item.Source {
	case llm.SourceFile:
		direct, _ := ctx.Value(directAPIKey{}).(bool)
		if !direct {
			return ContentBlock{}, true, fmt.Errorf("Vertex Claude cannot resolve direct provider file handles")
		}
		if item.Provider != "" && item.Provider != "anthropic" {
			return ContentBlock{}, true, fmt.Errorf("Claude cannot resolve %s provider file handle", item.Provider)
		}
		source.Type = "file"
		source.FileID = item.Data
	case llm.SourceURL:
		direct, _ := ctx.Value(directAPIKey{}).(bool)
		if !direct {
			return ContentBlock{}, true, fmt.Errorf("Vertex Claude protocol URL media requires inline data")
		}
		if kind == "document" && item.MimeType != "" && item.MimeType != "application/pdf" {
			return ContentBlock{}, true, fmt.Errorf("Claude URL documents require application/pdf")
		}
		source.Type = "url"
		source.URL = item.Data
	case llm.SourceBase64:
		if kind == "document" && item.MimeType == "text/plain" {
			data, err := base64.StdEncoding.DecodeString(item.Data)
			if err != nil || !utf8.Valid(data) {
				return ContentBlock{}, true, fmt.Errorf("Claude plain text document requires valid UTF-8 data")
			}
			source.Type, source.MediaType, source.Data = "text", "text/plain", string(data)
			return ContentBlock{Type: kind, Source: source}, true, nil
		}
		if kind == "document" && item.MimeType != "application/pdf" {
			return ContentBlock{}, true, fmt.Errorf("Claude inline documents require application/pdf or text/plain")
		}
		source.Type = "base64"
		source.MediaType = item.MimeType
		source.Data = item.Data
	default:
		return ContentBlock{}, true, fmt.Errorf("Claude unsupported media source: %s", item.Source)
	}
	return ContentBlock{Type: kind, Source: source}, true, nil
}
func toolContent(ctx context.Context, item llm.ContentItem) (ContentBlock, error) {
	if result, handled, err := protocolContent(ctx, item); handled {
		return result, err
	}
	if item.Type == llm.ContentTypeText {
		value := item.Data
		if value == "" {
			value = item.Text
		}
		return ContentBlock{Type: "text", Text: value}, nil
	}
	kind := ""
	switch item.Type {
	case llm.ContentTypeImage:
		kind = "image"
	case llm.ContentTypePDF:
		kind = "document"
	case llm.ContentTypeBinary:
		if strings.HasPrefix(item.MimeType, "image/") {
			kind = "image"
		} else if item.MimeType == "application/pdf" {
			kind = "document"
		}
	}
	if kind == "" || item.Source != llm.SourceBase64 {
		return ContentBlock{}, fmt.Errorf("unsupported Claude tool result media: %s/%s", item.Type, item.Source)
	}
	return ContentBlock{Type: kind, Source: &Source{Type: "base64", MediaType: item.MimeType, Data: item.Data}}, nil
}
