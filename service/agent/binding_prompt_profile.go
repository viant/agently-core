package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/protocol/binding"
	intake "github.com/viant/agently-core/protocol/intake"
	"github.com/viant/agently-core/workspace"
	embSchema "github.com/viant/embedius/schema"
)

func (s *Service) applySelectedPromptProfile(ctx context.Context, input *QueryInput, b *binding.Binding) error {
	if s == nil || input == nil || b == nil || s.promptRepo == nil {
		return nil
	}
	profile, err := s.selectedPromptProfileAny(ctx, input)
	if err != nil {
		return err
	}
	if profile == nil {
		return nil
	}
	if err := s.applyProfileKnowledge(ctx, input, b, profile); err != nil {
		return err
	}
	applyProfileExecutionRestrictions(b, profile)
	if input.Agent != nil && !input.Agent.Prompts.AllowsSelectedProfileInjection() {
		return nil
	}
	profileID := input.EffectiveIntentProfileID()
	msgs, err := profile.Render(ctx, s.mcpMgr, nil)
	if err != nil {
		return fmt.Errorf("render intake profile %q: %w", profileID, err)
	}
	for i, msg := range msgs {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "system") {
			continue
		}
		content := strings.TrimSpace(msg.Text)
		if content == "" {
			continue
		}
		uri := fmt.Sprintf("prompt://%s/message/%d", profileID, i)
		if hasDocumentURI(b.SystemDocuments.Items, uri) {
			continue
		}
		b.SystemDocuments.Items = append(b.SystemDocuments.Items, &binding.Document{
			Title:       strings.TrimSpace(profile.Name),
			PageContent: content,
			SourceURI:   uri,
			MimeType:    "text/markdown",
			Metadata: map[string]string{
				"kind":    "prompt_profile",
				"profile": profileID,
			},
		})
	}
	return nil
}

func applyProfileExecutionRestrictions(b *binding.Binding, profile *intake.Profile) {
	if b == nil || profile == nil || profile.Execution == nil || !profile.Execution.DisableDelegation {
		return
	}
	filtered := b.Tools.Signatures[:0]
	for _, definition := range b.Tools.Signatures {
		if definition == nil || isProfileDelegationTool(definition.Name) {
			continue
		}
		filtered = append(filtered, definition)
	}
	b.Tools.Signatures = filtered
}

func isProfileDelegationTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(name, "llm/agents:") ||
		strings.HasPrefix(name, "llm_agents-") ||
		strings.HasPrefix(name, "orchestration/plan:") ||
		strings.HasPrefix(name, "orchestration_plan-")
}

type profileKnowledgeMatchOutput struct {
	Documents []embSchema.Document `json:"documents"`
}

var profileMarkdownLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\(https?://[^)]+\)`)
var profileBareURLPattern = regexp.MustCompile(`https?://[^\s<>)\]]+`)
var profileMultiQueryPrefixPattern = regexp.MustCompile(`(?i)^\s*(?:explain\s+and\s+compare|compare|explain|describe|tell\s+me\s+about)\s+`)
var profileMultiQuerySplitPattern = regexp.MustCompile(`(?i)\s*(?:,|;|\band\b)\s*`)

func (s *Service) applyProfileKnowledge(ctx context.Context, input *QueryInput, b *binding.Binding, profile *intake.Profile) error {
	if s == nil || input == nil || b == nil || profile == nil || len(profile.Knowledge) == 0 {
		return nil
	}
	if s.registry == nil {
		return fmt.Errorf("profile %q knowledge matching requires the resources service", profile.ID)
	}
	for matchIndex, spec := range profile.Knowledge {
		if len(spec.RootIDs) == 0 {
			if spec.Required {
				return fmt.Errorf("profile %q knowledge[%d] requires at least one rootId", profile.ID, matchIndex)
			}
			continue
		}
		args := map[string]interface{}{"rootIds": append([]string(nil), spec.RootIDs...)}
		if spec.Path != "" {
			args["path"] = spec.Path
		}
		maxFragments := spec.MaxFragments
		if maxFragments <= 0 {
			maxFragments = spec.MaxDocuments
		}
		if maxFragments > 0 {
			args["maxDocuments"] = maxFragments
		}
		if spec.LimitBytes > 0 {
			args["limitBytes"] = spec.LimitBytes
		}
		if len(spec.Exclude) > 0 {
			args["exclude"] = append([]string(nil), spec.Exclude...)
		}
		if spec.NeighborFragmentsBefore > 0 {
			args["neighborFragmentsBefore"] = spec.NeighborFragmentsBefore
		}
		if spec.NeighborFragmentsAfter > 0 {
			args["neighborFragmentsAfter"] = spec.NeighborFragmentsAfter
		}
		queries := profileKnowledgeQueries(input.Query, spec.QueryMode, spec.MaxQueries)
		if spec.QueryCatalog != nil {
			var err error
			queries, err = profileKnowledgeCatalogQueries(input.Query, queries, spec.QueryCatalog, spec.MaxQueries)
			if err != nil {
				if spec.Required {
					return fmt.Errorf("profile %q knowledge[%d] query catalog: %w", profile.ID, matchIndex, err)
				}
				logx.Warnf("conversation", "intent.profile.knowledge query catalog profile=%q index=%d err=%v", profile.ID, matchIndex, err)
			}
		}
		groups := make([][]embSchema.Document, 0, len(queries))
		for queryIndex, matchQuery := range queries {
			queryArgs := cloneProfileKnowledgeArgs(args)
			queryArgs["query"] = matchQuery
			if maxFragments > 0 {
				queryArgs["maxDocuments"] = profileKnowledgeFragmentBudget(maxFragments, len(queries), queryIndex)
			}
			raw, err := s.registry.Execute(ctx, "resources:match", queryArgs)
			if err != nil {
				if spec.Required {
					return fmt.Errorf("profile %q knowledge[%d] match failed: %w", profile.ID, matchIndex, err)
				}
				continue
			}
			var queryMatches profileKnowledgeMatchOutput
			if err := json.Unmarshal([]byte(raw), &queryMatches); err != nil {
				if spec.Required {
					return fmt.Errorf("profile %q knowledge[%d] returned invalid JSON: %w", profile.ID, matchIndex, err)
				}
				continue
			}
			for docIndex := range queryMatches.Documents {
				if queryMatches.Documents[docIndex].Metadata == nil {
					queryMatches.Documents[docIndex].Metadata = map[string]interface{}{}
				}
				queryMatches.Documents[docIndex].Metadata["intentProfileQuery"] = matchQuery
			}
			groups = append(groups, queryMatches.Documents)
		}
		matched := profileKnowledgeMatchOutput{Documents: interleaveProfileKnowledgeGroups(groups)}
		logx.Infof("conversation", "intent.profile.knowledge matched profile=%q index=%d roots=%v documents=%d", strings.TrimSpace(profile.ID), matchIndex, spec.RootIDs, len(matched.Documents))
		kept := 0
		totalBytes := 0
		contentBudget := spec.MaxTotalBytes
		if contentBudget > 2048 {
			contentBudget -= 2048 // reserve room for the selected-document manifest
		}
		var selectedSources []string
		for docIndex, doc := range matched.Documents {
			logx.Infof("conversation", "intent.profile.knowledge candidate profile=%q index=%d document=%d score=%.6f source=%q", strings.TrimSpace(profile.ID), matchIndex, docIndex, doc.Score, profileKnowledgeDocumentURI(profile.ID, matchIndex, docIndex, doc.Metadata))
			if spec.MinScore != nil && doc.Score < float32(*spec.MinScore) {
				continue
			}
			uri := profileKnowledgeDocumentURI(profile.ID, matchIndex, docIndex, doc.Metadata)
			if hasDocumentURI(b.SystemDocuments.Items, uri) {
				continue
			}
			content := strings.TrimSpace(doc.PageContent)
			sourceTitle, sourceURL, fullContent := s.profileKnowledgeDocument(ctx, uri)
			useFullDocument := profileKnowledgeUseFullDocument(spec.DocumentMode, input.Query)
			if useFullDocument && strings.TrimSpace(fullContent) != "" {
				content = strings.TrimSpace(fullContent)
			} else if spec.NeighborFragmentsBefore > 0 || spec.NeighborFragmentsAfter > 0 {
				content = profileKnowledgeWithNeighbors(matched.Documents, doc, spec.NeighborFragmentsBefore, spec.NeighborFragmentsAfter)
			}
			if spec.MaxDocumentBytes > 0 {
				content = truncateProfileKnowledge(content, spec.MaxDocumentBytes)
			}
			if spec.CanonicalSourceOnly {
				content = stripProfileKnowledgeLinks(content)
			}
			logx.Infof("conversation", "intent.profile.knowledge canonical_source profile=%q document=%d uri=%q title=%q source_url=%q", strings.TrimSpace(profile.ID), docIndex, uri, sourceTitle, sourceURL)
			canonicalCitation := ""
			if sourceURL != "" {
				label := sourceTitle
				if label == "" {
					label = "Source article"
				}
				canonicalCitation = fmt.Sprintf("\n\nCanonical source article: [%s](%s)", label, sourceURL)
			}
			if contentBudget > 0 {
				remaining := contentBudget - totalBytes - len(canonicalCitation)
				if remaining <= 0 {
					break
				}
				content = truncateProfileKnowledge(content, remaining)
			}
			content = strings.TrimSpace(content) + canonicalCitation
			if strings.TrimSpace(content) == "" {
				continue
			}
			metadata := map[string]string{
				"kind":    "profile_knowledge",
				"profile": strings.TrimSpace(profile.ID),
				"score":   strconv.FormatFloat(float64(doc.Score), 'f', 6, 32),
			}
			if rootID := profileKnowledgeMetadataString(doc.Metadata, "rootId"); rootID != "" {
				metadata["rootId"] = rootID
			}
			if sourceURL != "" {
				metadata["sourceUrl"] = sourceURL
				label := sourceTitle
				if label == "" {
					label = profileKnowledgeTitle(uri)
				}
				selectedSources = append(selectedSources, fmt.Sprintf("- [%s](%s)", label, sourceURL))
			} else {
				selectedSources = append(selectedSources, "- "+profileKnowledgeTitle(uri))
			}
			b.SystemDocuments.Items = append(b.SystemDocuments.Items, &binding.Document{
				Title:       profileKnowledgeTitle(uri),
				PageContent: content,
				SourceURI:   uri,
				MimeType:    "text/markdown",
				Metadata:    metadata,
			})
			totalBytes += len(content)
			kept++
			if spec.MaxDocuments > 0 && kept >= spec.MaxDocuments {
				break
			}
		}
		if len(selectedSources) > 0 {
			manifest := "# Selected intent-profile knowledge\n\nUse all relevant documents below when the question spans multiple topics. Cite only their canonical links.\n\n" + strings.Join(selectedSources, "\n")
			if spec.MaxTotalBytes <= 0 || totalBytes+len(manifest) <= spec.MaxTotalBytes {
				b.SystemDocuments.Items = append(b.SystemDocuments.Items, &binding.Document{
					Title:       "Selected intent-profile knowledge",
					PageContent: manifest,
					SourceURI:   fmt.Sprintf("internal://intent-profile/%s/knowledge/%d", strings.TrimSpace(profile.ID), matchIndex),
					MimeType:    "text/markdown",
					Metadata: map[string]string{
						"kind":    "profile_knowledge_manifest",
						"profile": strings.TrimSpace(profile.ID),
					},
				})
				totalBytes += len(manifest)
			}
		}
		logx.Infof("conversation", "intent.profile.knowledge selected profile=%q index=%d kept=%d bytes=%d minScore=%v", strings.TrimSpace(profile.ID), matchIndex, kept, totalBytes, spec.MinScore)
	}
	return nil
}

type profileKnowledgeCatalogManifest struct {
	Articles []struct {
		Title string `json:"title"`
	} `json:"articles"`
}

type profileKnowledgeCatalogCandidate struct {
	title string
	score float64
}

func profileKnowledgeCatalogQueries(original string, queries []string, catalog *intake.QueryCatalog, maxQueries int) ([]string, error) {
	if catalog == nil || strings.TrimSpace(catalog.Path) == "" {
		return queries, nil
	}
	path := workspace.ResolvePathTemplate(strings.TrimSpace(catalog.Path))
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace.Root(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return queries, err
	}
	var manifest profileKnowledgeCatalogManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return queries, err
	}
	queryTokens := profileKnowledgeSignificantTokens(original)
	if len(queryTokens) == 0 {
		return queries, nil
	}
	var candidates []profileKnowledgeCatalogCandidate
	for _, article := range manifest.Articles {
		title := strings.TrimSpace(article.Title)
		if title == "" {
			continue
		}
		titleTokens := profileKnowledgeSignificantTokens(title)
		matched := 0
		for token := range queryTokens {
			if titleTokens[token] {
				matched++
			}
		}
		if matched == 0 {
			continue
		}
		candidates = append(candidates, profileKnowledgeCatalogCandidate{title: title, score: float64(matched) / float64(len(queryTokens))})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			// Prefer the more descriptive table-of-contents title when token
			// overlap ties; terse titles are more likely to be ambiguous.
			return len(candidates[i].title) > len(candidates[j].title)
		}
		return candidates[i].score > candidates[j].score
	})
	limit := catalog.MaxMatches
	if limit <= 0 {
		limit = 2
	}
	seen := map[string]bool{}
	for _, query := range queries {
		seen[strings.ToLower(strings.TrimSpace(query))] = true
	}
	added := 0
	for _, candidate := range candidates {
		if added >= limit || maxQueries > 0 && len(queries) >= maxQueries {
			break
		}
		expanded := candidate.title + ". User question: " + strings.TrimSpace(original)
		key := strings.ToLower(expanded)
		if seen[key] {
			continue
		}
		seen[key] = true
		queries = append(queries, expanded)
		added++
	}
	return queries, nil
}

var profileKnowledgeQueryStopWords = map[string]bool{
	"about": true, "does": true, "explain": true, "how": true, "is": true,
	"me": true, "the": true, "tell": true, "what": true, "works": true,
}

func profileKnowledgeSignificantTokens(value string) map[string]bool {
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	result := map[string]bool{}
	for _, field := range fields {
		if len(field) < 3 || profileKnowledgeQueryStopWords[field] {
			continue
		}
		result[field] = true
	}
	return result
}

func profileKnowledgeUseFullDocument(mode, query string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "full":
		return true
	case "explicit":
		query = strings.ToLower(query)
		for _, phrase := range []string{"full article", "whole article", "entire article", "complete article", "full text", "show the article", "read the article"} {
			if strings.Contains(query, phrase) {
				return true
			}
		}
	}
	return false
}

func cloneProfileKnowledgeArgs(input map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(input)+1)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func profileKnowledgeQueries(query, mode string, maxQueries int) []string {
	query = strings.TrimSpace(query)
	if query == "" || !strings.EqualFold(strings.TrimSpace(mode), "multi") {
		return []string{query}
	}
	trimmed := profileMultiQueryPrefixPattern.ReplaceAllString(query, "")
	parts := profileMultiQuerySplitPattern.Split(trimmed, -1)
	seen := map[string]bool{}
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), ".?!")
		if len(strings.Fields(part)) == 0 {
			continue
		}
		key := strings.ToLower(part)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, part)
		if maxQueries > 0 && len(result) >= maxQueries {
			break
		}
	}
	if len(result) <= 1 {
		return []string{query}
	}
	return result
}

func interleaveProfileKnowledgeGroups(groups [][]embSchema.Document) []embSchema.Document {
	var result []embSchema.Document
	for rank := 0; ; rank++ {
		added := false
		for _, group := range groups {
			if rank >= len(group) {
				continue
			}
			result = append(result, group[rank])
			added = true
		}
		if !added {
			break
		}
	}
	return result
}

func profileKnowledgeFragmentBudget(total, queryCount, queryIndex int) int {
	if total <= 0 {
		return 0
	}
	if queryCount <= 1 {
		return total
	}
	base := total / queryCount
	if base < 1 {
		base = 1
	}
	if queryIndex < total%queryCount {
		base++
	}
	return base
}

func profileKnowledgeWithNeighbors(candidates []embSchema.Document, anchor embSchema.Document, before, after int) string {
	if before < 0 {
		before = 0
	}
	if after < 0 {
		after = 0
	}
	anchorPath := profileKnowledgeDocumentPath(anchor.Metadata)
	if anchorPath == "" || before+after == 0 {
		return strings.TrimSpace(anchor.PageContent)
	}
	var sameDocument []embSchema.Document
	for _, candidate := range candidates {
		if profileKnowledgeDocumentPath(candidate.Metadata) == anchorPath {
			sameDocument = append(sameDocument, candidate)
		}
	}
	if len(sameDocument) <= 1 {
		return strings.TrimSpace(anchor.PageContent)
	}
	sort.SliceStable(sameDocument, func(i, j int) bool {
		return profileKnowledgeMetadataInt(sameDocument[i].Metadata, "start") < profileKnowledgeMetadataInt(sameDocument[j].Metadata, "start")
	})
	anchorFragment := profileKnowledgeMetadataString(anchor.Metadata, "fragmentId")
	anchorStart := profileKnowledgeMetadataInt(anchor.Metadata, "start")
	anchorIndex := -1
	for index, candidate := range sameDocument {
		if anchorFragment != "" && profileKnowledgeMetadataString(candidate.Metadata, "fragmentId") == anchorFragment {
			anchorIndex = index
			break
		}
		if anchorFragment == "" && profileKnowledgeMetadataInt(candidate.Metadata, "start") == anchorStart {
			anchorIndex = index
			break
		}
	}
	if anchorIndex < 0 {
		return strings.TrimSpace(anchor.PageContent)
	}
	from := anchorIndex - before
	if from < 0 {
		from = 0
	}
	to := anchorIndex + after + 1
	if to > len(sameDocument) {
		to = len(sameDocument)
	}
	parts := make([]string, 0, to-from)
	seen := map[string]bool{}
	for _, candidate := range sameDocument[from:to] {
		key := profileKnowledgeMetadataString(candidate.Metadata, "fragmentId")
		if key == "" {
			key = fmt.Sprintf("%s:%d", anchorPath, profileKnowledgeMetadataInt(candidate.Metadata, "start"))
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if content := strings.TrimSpace(candidate.PageContent); content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n\n")
}

func profileKnowledgeDocumentPath(metadata map[string]interface{}) string {
	for _, key := range []string{"path", "docId"} {
		if value := profileKnowledgeMetadataString(metadata, key); value != "" {
			return value
		}
	}
	return ""
}

func profileKnowledgeMetadataInt(metadata map[string]interface{}, key string) int {
	if metadata == nil {
		return 0
	}
	switch value := metadata[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := strconv.Atoi(value.String())
		return parsed
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(value))
		return parsed
	default:
		return 0
	}
}

func (s *Service) profileKnowledgeDocument(ctx context.Context, uri string) (string, string, string) {
	if s == nil || s.fs == nil || strings.TrimSpace(uri) == "" {
		return "", "", ""
	}
	readURI := strings.TrimSpace(uri)
	if strings.HasPrefix(readURI, "workspace://") {
		relative := strings.TrimPrefix(readURI, "workspace://")
		if strings.HasPrefix(relative, "localhost/") {
			readURI = "/" + strings.TrimPrefix(relative, "localhost/")
		} else if filepath.IsAbs(relative) {
			readURI = relative
		} else {
			readURI = filepath.Join(workspace.Root(), relative)
		}
	}
	data, err := s.fs.DownloadWithURL(ctx, readURI)
	if err != nil || len(data) == 0 {
		return "", "", ""
	}
	var title, sourceURL string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "title:") {
			title = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "title:")), `"'`)
		}
		if strings.HasPrefix(line, "sourceUrl:") {
			sourceURL = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "sourceUrl:")), `"'`)
		}
		if title != "" && sourceURL != "" {
			break
		}
	}
	return title, sourceURL, string(data)
}

func (s *Service) profileKnowledgeCanonicalSource(ctx context.Context, uri string) (string, string) {
	title, sourceURL, _ := s.profileKnowledgeDocument(ctx, uri)
	return title, sourceURL
}

func stripProfileKnowledgeLinks(content string) string {
	content = profileMarkdownLinkPattern.ReplaceAllString(content, "$1")
	content = profileBareURLPattern.ReplaceAllString(content, "")
	return strings.TrimSpace(content)
}

func truncateProfileKnowledge(content string, limit int) string {
	if limit <= 0 || len(content) <= limit {
		return content
	}
	const marker = "\n\n[Document truncated by intent-profile byte limit.]"
	bodyLimit := limit - len(marker)
	if bodyLimit <= 0 {
		bodyLimit = limit
	}
	data := []byte(content)[:bodyLimit]
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	result := strings.TrimSpace(string(data))
	if len(result)+len(marker) <= limit {
		result += marker
	}
	return result
}

func profileKnowledgeDocumentURI(profileID string, matchIndex, docIndex int, metadata map[string]interface{}) string {
	for _, key := range []string{"path", "docId", "fragmentId"} {
		if value := profileKnowledgeMetadataString(metadata, key); value != "" {
			return value
		}
	}
	return fmt.Sprintf("internal://profile-knowledge/%s/%d/%d", strings.TrimSpace(profileID), matchIndex, docIndex)
}

func profileKnowledgeMetadataString(metadata map[string]interface{}, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func profileKnowledgeTitle(uri string) string {
	name := strings.TrimSpace(filepath.Base(uri))
	if name == "" || name == "." || name == "/" {
		return "Profile knowledge"
	}
	return name
}

var _ = intake.Profile{}
