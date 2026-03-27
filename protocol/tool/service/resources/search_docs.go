package resources

import (
	pathpkg "path"
	"strings"

	"github.com/viant/agently-core/internal/agent/systemdoc"
	aug "github.com/viant/agently-core/service/augmenter"
	embSchema "github.com/viant/embedius/schema"
)

func uniqueMatchedDocuments(docs []embSchema.Document, max int) []MatchedDocument {
	if len(docs) == 0 {
		return nil
	}
	type entry struct {
		doc MatchedDocument
		idx int
	}
	seen := map[string]entry{}
	order := make([]string, 0, len(docs))
	for idx, doc := range docs {
		uri := documentMetadataPath(doc.Metadata)
		if uri == "" {
			continue
		}
		current := MatchedDocument{
			URI:    uri,
			RootID: documentRootID(doc.Metadata),
			Score:  doc.Score,
		}
		if existing, ok := seen[uri]; ok {
			if current.Score > existing.doc.Score {
				seen[uri] = entry{doc: current, idx: existing.idx}
			}
			continue
		}
		seen[uri] = entry{doc: current, idx: idx}
		order = append(order, uri)
	}
	if len(seen) == 0 {
		return nil
	}
	result := make([]MatchedDocument, 0, len(seen))
	for _, uri := range order {
		if rec, ok := seen[uri]; ok {
			result = append(result, rec.doc)
			if max > 0 && len(result) >= max {
				break
			}
		}
	}
	return result
}

// effectiveLimitBytes returns the per-page byte cap (default 7000, upper bound 200k)
func effectiveLimitBytes(limit int) int {
	if limit <= 0 {
		return 7000
	}
	if limit > 200000 {
		return 200000
	}
	return limit
}

// effectiveCursor normalizes the page cursor (1..N)
func effectiveCursor(cursor int) int {
	if cursor <= 0 {
		return 1
	}
	return cursor
}

// selectDocPage splits documents into pages of at most limitBytes (based on formatted content length) and returns the selected page.
func selectDocPage(docs []embSchema.Document, limitBytes int, cursor int, trimPrefix string) ([]embSchema.Document, bool) {
	if limitBytes <= 0 || len(docs) == 0 {
		return nil, false
	}
	pages := make([][]embSchema.Document, 0, 4)
	var cur []embSchema.Document
	used := 0
	for _, d := range docs {
		loc := documentLocation(d, trimPrefix)
		formatted := formatDocument(loc, d.PageContent)
		fragBytes := len(formatted)
		if fragBytes > limitBytes {
			if len(cur) > 0 {
				pages = append(pages, cur)
				cur = nil
				used = 0
			}
			pages = append(pages, []embSchema.Document{d})
			continue
		}
		if used+fragBytes > limitBytes {
			pages = append(pages, cur)
			cur = nil
			used = 0
		}
		cur = append(cur, d)
		used += fragBytes
	}
	if len(cur) > 0 {
		pages = append(pages, cur)
	}
	if len(pages) == 0 {
		return nil, false
	}
	if cursor < 1 {
		cursor = 1
	}
	if cursor > len(pages) {
		cursor = len(pages)
	}
	sel := pages[cursor-1]
	hasNext := cursor < len(pages)
	return sel, hasNext
}

// formatDocument mirrors augmenter.addDocumentContent formatting.
func formatDocument(loc string, content string) string {
	ext := strings.Trim(pathpkg.Ext(loc), ".")
	return "file: " + loc + "\n```" + ext + "\n" + content + "\n````\n\n"
}

// augmenterDocumentsSize computes combined size using augmenter.Document.Size()
func augmenterDocumentsSize(docs []embSchema.Document) int {
	total := 0
	for _, d := range docs {
		total += aug.Document(d).Size()
	}
	return total
}

// getStringFromMetadata safely extracts a string value from metadata map.
func getStringFromMetadata(metadata map[string]any, key string) string {
	if value, ok := metadata[key]; ok {
		if text, ok := value.(string); ok {
			return text
		}
	}
	return ""
}

func totalFormattedBytes(docs []embSchema.Document, trimPrefix string) int {
	total := 0
	for _, d := range docs {
		loc := documentLocation(d, trimPrefix)
		total += len(formatDocument(loc, d.PageContent))
	}
	return total
}

func buildDocumentContent(docs []embSchema.Document, trimPrefix string) string {
	if len(docs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, doc := range docs {
		loc := documentLocation(doc, trimPrefix)
		_, _ = b.WriteString(formatDocument(loc, doc.PageContent))
	}
	return b.String()
}

func documentLocation(doc embSchema.Document, trimPrefix string) string {
	loc := strings.TrimPrefix(getStringFromMetadata(doc.Metadata, "path"), trimPrefix)
	if loc == "" {
		loc = getStringFromMetadata(doc.Metadata, "docId")
	}
	return loc
}

func filterSystemDocuments(docs []embSchema.Document, prefixes []string) []embSchema.Document {
	if len(prefixes) == 0 || len(docs) == 0 {
		return nil
	}
	out := make([]embSchema.Document, 0, len(docs))
	for _, doc := range docs {
		if systemdoc.Matches(prefixes, documentMetadataPath(doc.Metadata)) {
			out = append(out, doc)
		}
	}
	return out
}

func documentMetadataPath(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	for _, key := range []string{"path", "docId", "fragmentId"} {
		if v, ok := metadata[key]; ok {
			if s, _ := v.(string); strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

func documentRootID(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	if v, ok := metadata["rootId"]; ok {
		if s, _ := v.(string); strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func buildDocumentRootsMap(docs []embSchema.Document) map[string]string {
	if len(docs) == 0 {
		return nil
	}
	roots := make(map[string]string)
	for _, doc := range docs {
		path := documentMetadataPath(doc.Metadata)
		rootID := documentRootID(doc.Metadata)
		if path != "" && rootID != "" {
			roots[path] = rootID
		}
	}
	if len(roots) == 0 {
		return nil
	}
	return roots
}
