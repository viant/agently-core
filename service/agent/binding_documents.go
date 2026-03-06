package agent

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"path"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	intmodel "github.com/viant/agently-core/internal/finder/model"
	"github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/prompt"
	padapter "github.com/viant/agently-core/protocol/prompt/adapter"
	"github.com/viant/agently-core/service/elicitation"
	executil "github.com/viant/agently-core/service/shared/executil"
)

func (s *Service) appendAgentDirectoryDoc(ctx context.Context, input *QueryInput, docs *prompt.Documents) {
	if s == nil || input == nil || input.Agent == nil || docs == nil {
		debugf("delegation.directory skip missing service/input/agent/docs")
		return
	}
	if input.Agent.Delegation == nil || !input.Agent.Delegation.Enabled {
		debugf("delegation.directory disabled agent_id=%q", strings.TrimSpace(input.Agent.ID))
		return
	}
	// Avoid duplicate injection.
	const sourceURI = "internal://llm/agents/list"
	if hasDocumentURI(docs.Items, sourceURI) {
		debugf("delegation.directory skip already_present agent_id=%q", strings.TrimSpace(input.Agent.ID))
		return
	}
	items, err := s.listPublishedAgents(ctx)
	if err != nil {
		debugf("delegation.directory list_error agent_id=%q err=%v", strings.TrimSpace(input.Agent.ID), err)
		return
	}
	if len(items) == 0 {
		debugf("delegation.directory list_empty agent_id=%q", strings.TrimSpace(input.Agent.ID))
		return
	}
	var bld strings.Builder
	bld.WriteString("# Available Agents\n\n")
	for _, it := range items {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			name = strings.TrimSpace(it.Identity.Name)
		}
		if name == "" {
			continue
		}
		desc := strings.TrimSpace(it.Description)
		if desc == "" {
			desc = strings.TrimSpace(it.Profile.Description)
		}
		bld.WriteString("- ")
		bld.WriteString(name)
		if id := strings.TrimSpace(it.ID); id != "" && id != name {
			bld.WriteString(" (`")
			bld.WriteString(id)
			bld.WriteString("`)")
		}
		if desc != "" {
			bld.WriteString(": ")
			bld.WriteString(desc)
		}
		bld.WriteString("\n")
	}
	doc := &prompt.Document{
		Title:       "agents/directory",
		PageContent: strings.TrimSpace(bld.String()),
		SourceURI:   sourceURI,
		MimeType:    "text/markdown",
		Metadata:    map[string]string{"kind": "agents_directory"},
	}
	docs.Items = append(docs.Items, doc)
	debugf("delegation.directory injected agent_id=%q count=%d", strings.TrimSpace(input.Agent.ID), len(items))
}

func (s *Service) listPublishedAgents(ctx context.Context) ([]*agent.Agent, error) {
	type allFinder interface {
		All() []*agent.Agent
	}
	if s != nil && s.agentFinder != nil {
		if finder, ok := s.agentFinder.(allFinder); ok {
			items := finder.All()
			if len(items) == 0 {
				return nil, nil
			}
			filtered := make([]*agent.Agent, 0, len(items))
			for _, item := range items {
				if item == nil || item.Profile == nil || !item.Profile.Publish || item.Internal {
					continue
				}
				filtered = append(filtered, item)
			}
			return filtered, nil
		}
	}
	if s == nil || s.registry == nil {
		return nil, nil
	}
	raw, err := s.registry.Execute(ctx, "llm/agents:list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var lo struct {
		Items []struct {
			ID          string `json:"id"`
			Name        string `json:"name,omitempty"`
			Description string `json:"description,omitempty"`
			Summary     string `json:"summary,omitempty"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &lo); err != nil {
		return nil, err
	}
	if len(lo.Items) == 0 {
		return nil, nil
	}
	result := make([]*agent.Agent, 0, len(lo.Items))
	for _, item := range lo.Items {
		a := &agent.Agent{
			Identity: agent.Identity{
				ID:   strings.TrimSpace(item.ID),
				Name: strings.TrimSpace(item.Name),
			},
			Description: strings.TrimSpace(item.Description),
			Profile: &agent.Profile{
				Publish:     true,
				Name:        strings.TrimSpace(item.Name),
				Description: strings.TrimSpace(item.Description),
			},
		}
		if a.Description == "" {
			a.Description = strings.TrimSpace(item.Summary)
		}
		if a.Profile.Description == "" {
			a.Profile.Description = strings.TrimSpace(item.Summary)
		}
		result = append(result, a)
	}
	return result, nil
}

func (s *Service) buildTraces(tr apiconv.Transcript) map[string]*prompt.Trace {
	var result = make(map[string]*prompt.Trace)
	for _, turn := range tr {
		if turn == nil {
			continue
		}
		for _, m := range turn.GetMessages() {
			if m == nil {
				continue
			}
			// Assistant model-call response
			if m.ModelCall != nil && m.ModelCall.TraceId != nil {
				id := strings.TrimSpace(*m.ModelCall.TraceId)
				if id != "" {
					key := prompt.KindResponse.Key(id)
					result[key] = &prompt.Trace{ID: id, Kind: prompt.KindResponse, At: m.CreatedAt}
				}
				continue
			}
			// Tool-call message
			if tc := messageToolCall(m); tc != nil {
				opID := strings.TrimSpace(tc.OpId)
				if opID != "" {
					respId := ""
					if tc.TraceId != nil {
						respId = strings.TrimSpace(*tc.TraceId)
					}
					key := prompt.KindToolCall.Key(opID)
					result[key] = &prompt.Trace{ID: respId, Kind: prompt.KindToolCall, At: m.CreatedAt}
				}
				continue
			}

			// User/assistant text message
			if strings.ToLower(strings.TrimSpace(m.Type)) == "text" && m.Content != nil && *m.Content != "" {
				ckey := prompt.KindContent.Key(*m.Content)
				// Use a stable "effective at" timestamp for queued turns:
				// queued user messages may be persisted before the prior assistant
				// response exists, so comparing raw message.CreatedAt to the anchor
				// can incorrectly exclude the current prompt from continuation.
				// Prefer the later of turn.CreatedAt and message.CreatedAt.
				at := m.CreatedAt
				if !turn.CreatedAt.IsZero() && turn.CreatedAt.After(at) {
					at = turn.CreatedAt
				}
				result[ckey] = &prompt.Trace{ID: ckey, Kind: prompt.KindContent, At: at}
			}
		}
	}
	return result
}

// mergeElicitationPayloadIntoContext folds the most recent JSON object
// payloads from user elicitation messages into the binding context so
// downstream plans can see resolved inputs (e.g., workdir). Later
// messages win on key collision.
func mergeElicitationPayloadIntoContext(h prompt.History, ctxPtr *map[string]interface{}) {
	if ctxPtr == nil {
		return
	}
	if *ctxPtr == nil {
		*ctxPtr = map[string]interface{}{}
	}
	ctx := *ctxPtr

	// Helper to process a slice of messages in order.
	consume := func(msgs []*prompt.Message) {
		for _, m := range msgs {
			if m == nil || m.Kind != prompt.MessageKindElicitAnswer {
				continue
			}
			raw := strings.TrimSpace(m.Content)
			if raw == "" || !strings.HasPrefix(raw, "{") {
				continue
			}
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(raw), &payload); err != nil || len(payload) == 0 {
				continue
			}
			if elicitation.DebugEnabled() {
				log.Printf("[debug][elicitation] merge payloadKeys=%v", elicitation.PayloadKeys(payload))
			}
			for k, v := range payload {
				ctx[k] = v
			}
			// Lightweight alias normalization for common elicitation synonyms.
			// Helps when LLM varies field names across successive elicitations.
			if _, ok := ctx["favoriteColor"]; !ok {
				if v, ok := firstValue(payload, "favoriteColor", "favorite_color", "favColor", "fav_color", "color"); ok {
					ctx["favoriteColor"] = v
				}
			}
			if _, ok := ctx["color"]; !ok {
				if v, ok := firstValue(payload, "color", "favoriteColor", "favorite_color", "favColor", "fav_color"); ok {
					ctx["color"] = v
				}
			}
			if _, ok := ctx["shade"]; !ok {
				if v, ok := firstValue(payload, "shade", "shadeOrVariant", "variant"); ok {
					ctx["shade"] = v
				}
			}
			if _, ok := ctx["detailLevel"]; !ok {
				if v, ok := firstValue(payload, "detailLevel", "detail_level", "style", "tone", "toneOrVibe", "vibe"); ok {
					ctx["detailLevel"] = v
				}
			}
			if _, ok := ctx["style"]; !ok {
				if v, ok := firstValue(payload, "style", "detailLevel", "detail_level", "tone", "toneOrVibe", "vibe"); ok {
					ctx["style"] = v
				}
			}
			if _, ok := ctx["descriptionStyle"]; !ok {
				if v, ok := firstValue(payload, "descriptionStyle", "description_style", "style", "detailLevel", "detail_level", "tone", "toneOrVibe", "vibe"); ok {
					ctx["descriptionStyle"] = v
				}
			}
			if elicitation.DebugEnabled() {
				log.Printf("[debug][elicitation] ctxKeys=%v", elicitation.PayloadKeys(ctx))
			}
		}
	}

	for _, t := range h.Past {
		if t == nil {
			continue
		}
		consume(t.Messages)
	}
	if h.Current != nil {
		consume(h.Current.Messages)
	}
}

func firstValue(payload map[string]interface{}, keys ...string) (interface{}, bool) {
	if len(payload) == 0 {
		return nil, false
	}
	lower := map[string]interface{}{}
	for k, v := range payload {
		lower[strings.ToLower(strings.TrimSpace(k))] = v
	}
	for _, k := range keys {
		kk := strings.ToLower(strings.TrimSpace(k))
		if v, ok := lower[kk]; ok {
			return v, true
		}
	}
	return nil, false
}

// fetchConversationWithRetry attempts to fetch a conversation up to three times,
// applying a short exponential backoff on transient errors. It returns an error
// when the conversation is missing or on non-transient failures.
func (s *Service) fetchConversationWithRetry(ctx context.Context, id string, options ...apiconv.Option) (*apiconv.Conversation, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		conv, err := s.conversation.GetConversation(ctx, id, options...)
		if err == nil {
			if conv == nil {
				lastErr = fmt.Errorf("conversation not found: %s", strings.TrimSpace(id))
				// Read-after-write race: conversation may not be visible yet.
				if ctx.Err() != nil || attempt == 2 {
					break
				}
				delay := 200 * time.Millisecond << attempt
				select {
				case <-time.After(delay):
					continue
				case <-ctx.Done():
					return nil, fmt.Errorf("conversation fetch canceled: %w", lastErr)
				}
			}
			return conv, nil
		}
		lastErr = err
		// Do not keep retrying if context is done
		if ctx.Err() != nil {
			break
		}
		if !isTransientDBOrNetworkError(err) || attempt == 2 {
			break
		}
		// 200ms, 400ms backoff (final attempt follows immediately)
		delay := 200 * time.Millisecond << attempt
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, fmt.Errorf("conversation fetch canceled: %w", err)
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("failed to fetch conversation: %w", lastErr)
	}
	return nil, fmt.Errorf("conversation not found: %s", strings.TrimSpace(id))
}

// isTransientDBOrNetworkError classifies intermittent DB/driver/network failures
// that are commonly resolved with a short retry.
func isTransientDBOrNetworkError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout"),
		strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "deadline exceeded"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "driver: bad connection"),
		strings.Contains(msg, "too many connections"),
		strings.Contains(msg, "server closed idle connection"),
		strings.Contains(msg, "deadlock"),
		strings.Contains(msg, "lock wait timeout"),
		strings.Contains(msg, "transaction aborted"),
		strings.Contains(msg, "temporary network error"),
		strings.Contains(msg, "network is unreachable"):
		return true
	}
	return false
}

func (s *Service) normalizeDocURIs(docs *prompt.Documents, trim string) {
	if docs == nil || len(docs.Items) == 0 {
		return
	}
	trim = strings.TrimSpace(trim)
	if trim == "" {
		return
	}
	// Ensure trailing slash for precise trimming
	if !strings.HasSuffix(trim, "/") {
		trim += "/"
	}
	for _, d := range docs.Items {
		if d == nil {
			continue
		}
		uri := strings.TrimSpace(d.SourceURI)
		if uri == "" {
			continue
		}
		if strings.HasPrefix(uri, trim) {
			d.SourceURI = strings.TrimPrefix(uri, trim)
		}
	}
}

func (s *Service) appendTranscriptSystemDocs(tr apiconv.Transcript, b *prompt.Binding) {
	if b == nil {
		return
	}
	systemDocs := transcriptSystemDocuments(tr)
	if len(systemDocs) == 0 {
		return
	}
	if b.SystemDocuments.Items == nil {
		b.SystemDocuments.Items = []*prompt.Document{}
	}
	seen := map[string]bool{}
	contentHashes := map[string]bool{}
	for _, doc := range b.SystemDocuments.Items {
		if key := systemDocDedupKey(doc); key != "" {
			seen[key] = true
		}
		if hash := systemDocContentHash(doc); hash != "" {
			contentHashes[hash] = true
		}
	}
	for _, doc := range systemDocs {
		if doc == nil {
			continue
		}
		key := systemDocDedupKey(doc)
		if key != "" {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		if hash := systemDocContentHash(doc); hash != "" {
			if contentHashes[hash] {
				continue
			}
			contentHashes[hash] = true
		}
		b.SystemDocuments.Items = append(b.SystemDocuments.Items, doc)
	}
}

// allowContinuationPreview reports whether continuation preview formatting is
// enabled for the selected model on this turn. It resolves the effective model
// from QueryInput (ModelOverride > Agent.Model) and inspects the provider
// config option EnableContinuationFormat when available.
func (s *Service) allowContinuationPreview(ctx context.Context, input *QueryInput) bool {
	if s == nil || input == nil {
		return false
	}
	modelName := ""
	if strings.TrimSpace(input.ModelOverride) != "" {
		modelName = strings.TrimSpace(input.ModelOverride)
	} else if input.Agent != nil {
		modelName = strings.TrimSpace(input.Agent.Model)
	}
	if modelName == "" || s.llm == nil {
		return false
	}
	f := s.llm.ModelFinder()
	if f == nil {
		return false
	}
	if mf, ok := f.(*intmodel.Finder); ok {
		if cfg := mf.ConfigByIDOrModel(modelName); cfg != nil {
			return cfg.Options.EnableContinuationFormat
		}
	}
	return false
}

func transcriptSystemDocuments(tr apiconv.Transcript) []*prompt.Document {
	if len(tr) == 0 {
		return nil
	}
	var docs []*prompt.Document
	seen := map[string]bool{}
	for _, turn := range tr {
		if turn == nil || len(turn.GetMessages()) == 0 {
			continue
		}
		for _, msg := range turn.GetMessages() {
			doc := toSystemDocument(turn, msg)
			if doc == nil {
				continue
			}
			key := systemDocDedupKey(doc)
			if key != "" {
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			docs = append(docs, doc)
		}
	}
	return docs
}

func toSystemDocument(turn *apiconv.Turn, msg *apiconv.Message) *prompt.Document {
	if msg == nil || !hasSystemDocTag(msg) {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "system") {
		return nil
	}
	content := strings.TrimSpace(msg.GetContent())
	if content == "" {
		return nil
	}
	source := extractSystemDocSource(msg, content)
	meta := map[string]string{
		"messageId": strings.TrimSpace(msg.Id),
	}
	if turn != nil && strings.TrimSpace(turn.Id) != "" {
		meta["turnId"] = strings.TrimSpace(turn.Id)
	}
	if source != "" {
		meta["source"] = source
	}
	return &prompt.Document{
		Title:       deriveSystemDocTitle(source),
		PageContent: content,
		SourceURI:   source,
		MimeType:    inferMimeTypeFromSource(source),
		Metadata:    meta,
	}
}

func extractSystemDocSource(msg *apiconv.Message, content string) string {
	if msg != nil && msg.ContextSummary != nil {
		if v := strings.TrimSpace(*msg.ContextSummary); v != "" {
			return v
		}
	}
	firstLine := ""
	if content != "" {
		parts := strings.SplitN(content, "\n", 2)
		if len(parts) > 0 {
			firstLine = strings.TrimSpace(parts[0])
		}
	}
	if firstLine != "" && strings.HasPrefix(strings.ToLower(firstLine), "file:") {
		return strings.TrimSpace(firstLine[len("file:"):])
	}
	return ""
}

func deriveSystemDocTitle(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "System Document"
	}
	base := strings.TrimSpace(path.Base(source))
	if base == "" || base == "." || base == "/" {
		return source
	}
	return base
}

func inferMimeTypeFromSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(path.Ext(strings.TrimSpace(source)))) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".sql":
		return "text/plain"
	case ".go", ".py", ".ts", ".tsx", ".js", ".java", ".rb":
		return "text/plain"
	}
	return "text/markdown"
}

func hasSystemDocTag(msg *apiconv.Message) bool {
	if msg == nil {
		return false
	}
	if msg.Tags != nil {
		for _, tag := range strings.Split(*msg.Tags, ",") {
			if strings.EqualFold(strings.TrimSpace(tag), executil.SystemDocumentTag) {
				return true
			}
		}
	}
	mode := ""
	if msg.Mode != nil {
		mode = *msg.Mode
	}
	if strings.EqualFold(strings.TrimSpace(mode), executil.SystemDocumentMode) {
		return true
	}
	content := strings.ToLower(strings.TrimSpace(msg.GetContent()))
	return strings.HasPrefix(content, "file:")
}

func systemDocKey(doc *prompt.Document) string {
	if doc == nil {
		return ""
	}
	if v := strings.TrimSpace(doc.SourceURI); v != "" {
		return v
	}
	if doc.Metadata != nil {
		if id := strings.TrimSpace(doc.Metadata["messageId"]); id != "" {
			return id
		}
	}
	return strings.TrimSpace(doc.Title)
}

func systemDocDedupKey(doc *prompt.Document) string {
	if doc == nil {
		return ""
	}
	if key := systemDocKey(doc); key != "" {
		return key
	}
	title := strings.TrimSpace(doc.Title)
	content := strings.TrimSpace(doc.PageContent)
	if title == "" && content == "" {
		return ""
	}
	sum := md5.Sum([]byte(title + "::" + content))
	return hex.EncodeToString(sum[:])
}

func systemDocContentHash(doc *prompt.Document) string {
	if doc == nil {
		return ""
	}
	content := strings.TrimSpace(doc.PageContent)
	if content == "" {
		return ""
	}
	sum := md5.Sum([]byte(content))
	return hex.EncodeToString(sum[:])
}

func baseName(uri string) string {
	if uri == "" {
		return ""
	}
	if b := path.Base(uri); b != "." && b != "/" {
		return b
	}
	return uri
}

// attachNonTextUserDocs scans user documents and adds non-text docs as attachments.
// It avoids duplicating content in user templates, which now render references only.
func (s *Service) attachNonTextUserDocs(ctx context.Context, b *prompt.Binding) {
	if b == nil || len(b.Documents.Items) == 0 {
		return
	}
	for _, d := range b.Documents.Items {
		uri := strings.TrimSpace(d.SourceURI)
		if uri == "" {
			continue
		}
		mime := mimeFromExt(strings.ToLower(strings.TrimPrefix(path.Ext(uri), ".")))
		if !isNonTextMime(mime) {
			continue
		}
		var data []byte
		// Skip MCP URIs for binding attachments; handled via resources tools on-demand
		if !strings.HasPrefix(strings.ToLower(uri), "mcp:") {
			if raw, err := s.fs.DownloadWithURL(ctx, uri); err == nil && len(raw) > 0 {
				data = raw
			}
		}
		if len(data) == 0 {
			continue
		}
		b.Task.Attachments = append(b.Task.Attachments, &prompt.Attachment{
			Name: baseName(uri), URI: uri, Mime: mime, Data: data,
		})
	}
}

func isNonTextMime(m string) bool {
	switch m {
	case "application/pdf", "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/svg+xml":
		return true
	}
	return false
}

func mimeFromExt(ext string) string {
	switch ext {
	case "pdf":
		return "application/pdf"
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "bmp":
		return "image/bmp"
	case "svg":
		return "image/svg+xml"
	default:
		return "text/plain"
	}
}

func (s *Service) buildDocumentsBinding(ctx context.Context, input *QueryInput, isSystem bool) (prompt.Documents, error) {
	var docs prompt.Documents
	var knowledge []*agent.Knowledge
	if isSystem {
		knowledge = input.Agent.SystemKnowledge
	} else {
		knowledge = input.Agent.Knowledge
	}

	matchedDocs, err := s.matchDocuments(ctx, input, knowledge)
	if err != nil {
		return docs, err
	}

	docs.Items = padapter.FromSchemaDocs(matchedDocs)
	return docs, nil
}

// normalizeUserTaskContent trims whitespace and strips a leading
// "Task:" wrapper (case-insensitive, on the first line) so we can
// detect semantically equivalent user instructions such as a raw
// query and a later "Task:" form.
func normalizeUserTaskContent(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Fast path: no Task: prefix on first line.
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "task:") && !strings.Contains(lower, "\n") {
		return s
	}
	lines := strings.SplitN(s, "\n", 2)
	if len(lines) == 0 {
		return s
	}
	first := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(strings.ToLower(first), "task:") {
		return s
	}
	if len(lines) == 1 {
		// Only "Task:" line present; treat as empty content.
		return ""
	}
	// Use the remainder as the normalized task content.
	return strings.TrimSpace(lines[1])
}
