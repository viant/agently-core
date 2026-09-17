package resources

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/viant/agently-core/protocol/binding"
	embSchema "github.com/viant/embedius/schema"
)

// DocumentSelection is a generic post-match policy. It operates only on
// documents already returned by resources:match; it does not interpret the
// user query or source-specific document formats.
type DocumentSelection struct {
	MinScore      *float64
	MaxDocuments  int
	MaxTotalBytes int
}

// BindingDocuments maps structured semantic-match results to ordinary binding
// documents. Document metadata is preserved from the resource index so callers
// can use neutral keys such as document.title and source.url without reopening
// source files at request time.
func BindingDocuments(documents []embSchema.Document, selection DocumentSelection) []*binding.Document {
	seen := map[string]struct{}{}
	var result []*binding.Document
	used := 0
	for _, doc := range documents {
		if selection.MinScore != nil && doc.Score < float32(*selection.MinScore) {
			continue
		}
		uri := documentURI(doc.Metadata)
		if uri == "" {
			continue
		}
		if _, ok := seen[uri]; ok {
			continue
		}
		content := strings.TrimSpace(doc.PageContent)
		if content == "" {
			continue
		}
		if selection.MaxTotalBytes > 0 && used+len(content) > selection.MaxTotalBytes {
			remaining := selection.MaxTotalBytes - used
			if remaining <= 0 {
				break
			}
			content = truncateUTF8(content, remaining)
		}
		metadata := map[string]string{"kind": "resource_match", "score": strconv.FormatFloat(float64(doc.Score), 'f', 6, 32)}
		for _, key := range []string{"rootId", "document.id", "document.title", "source.url", "source.updatedAt", "source.locale"} {
			if value := metadataString(doc.Metadata, key); value != "" {
				metadata[key] = value
			}
		}
		title := metadata["document.title"]
		if title == "" {
			title = filepath.Base(uri)
		}
		result = append(result, &binding.Document{Title: title, SourceURI: uri, PageContent: content, MimeType: "text/markdown", Metadata: metadata})
		seen[uri] = struct{}{}
		used += len(content)
		if selection.MaxDocuments > 0 && len(result) >= selection.MaxDocuments {
			break
		}
	}
	return result
}

func documentURI(metadata map[string]any) string {
	for _, key := range []string{"path", "docId", "document.id", "fragmentId"} {
		if value := metadataString(metadata, key); value != "" {
			return value
		}
	}
	return ""
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	switch value := metadata[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		return ""
	}
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit || limit <= 0 {
		return value
	}
	data := []byte(value)[:limit]
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	return strings.TrimSpace(string(data))
}
