// Package toolselection holds discovery and schema activation for one turn.
// Activation changes model exposure only; tool execution retains its normal guards.
package toolselection

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/protocol/primitive"
	"github.com/viant/agently-core/runtime/requestctx"
)

type key struct{}

type Candidate struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type State struct {
	principal, conversation, turn string
	canonicalUser, provider       string
	catalog                       *primitive.Catalog[Candidate]
	catalogDigest                 [32]byte
	catalogGeneration             uint64
	allowed                       map[string]bool
	mu                            sync.RWMutex
	selected                      map[string]bool
}

// WithState starts a new selection scope from the caller's already authorized
// candidates. Call once per turn and retain the context across model iterations.
func WithState(ctx context.Context, candidates []llm.Tool) context.Context {
	meta, _ := requestctx.TurnMetaFromContext(ctx)
	s := &State{principal: auth.EffectiveUserID(ctx), conversation: requestctx.ConversationIDFromContext(ctx), turn: meta.TurnID, allowed: map[string]bool{}, selected: map[string]bool{}}
	s.canonicalUser, s.provider = auth.CanonicalUserID(ctx), auth.Provider(ctx)
	s.SetCandidates(candidates)
	return context.WithValue(ctx, key{}, s)
}

// SetCandidates refreshes metadata from the normal authorized binding pipeline.
// Removed candidates also lose activation; this method grants no execution rights.
func (s *State) SetCandidates(candidates []llm.Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allowed = map[string]bool{}
	var metadata []Candidate
	for _, tool := range candidates {
		name := tool.Definition.Name
		if name == "" || s.allowed[name] {
			continue
		}
		s.allowed[name] = true
		description := []rune(tool.Definition.Description)
		if len(description) > 512 {
			description = append(description[:511], '…')
		}
		metadata = append(metadata, Candidate{Name: name, Description: string(description)})
	}
	sort.Slice(metadata, func(i, j int) bool { return metadata[i].Name < metadata[j].Name })
	encoded, _ := json.Marshal(metadata)
	digest := sha256.Sum256(encoded)
	if s.catalog == nil || s.catalogDigest != digest {
		items := make([]primitive.CatalogItem[Candidate], 0, len(metadata))
		for _, item := range metadata {
			items = append(items, primitive.CatalogItem[Candidate]{Name: item.Name, Description: item.Description, Namespace: mcpname.Name(mcpname.Canonical(item.Name)).Service(), Value: item})
		}
		s.catalogGeneration++
		s.catalog = primitive.NewCatalog(fmt.Sprintf("tool-selection:%p:%d:%x", s, s.catalogGeneration, digest), items)
		s.catalogDigest = digest
	}
	for name := range s.selected {
		if !s.allowed[name] {
			delete(s.selected, name)
		}
	}
}

// FromContext rejects inherited state after a principal or turn change.
func FromContext(ctx context.Context) (*State, bool) {
	if ctx == nil || ctx.Err() != nil {
		return nil, false
	}
	s, ok := ctx.Value(key{}).(*State)
	if !ok || s == nil {
		return nil, false
	}
	meta, _ := requestctx.TurnMetaFromContext(ctx)
	if s.principal != auth.EffectiveUserID(ctx) || s.canonicalUser != auth.CanonicalUserID(ctx) || s.provider != auth.Provider(ctx) || s.conversation != requestctx.ConversationIDFromContext(ctx) || s.turn != meta.TurnID {
		return nil, false
	}
	return s, true
}

// List returns bounded metadata only, never input/output schemas.
func (s *State) List(query string, offset, limit int) ([]Candidate, bool, error) {
	if offset < 0 || limit < 0 || len(query) > 256 {
		return nil, false, fmt.Errorf("invalid tool discovery bounds")
	}
	page, err := s.ListPage(context.Background(), primitive.CatalogQuery{Keywords: []string{query}, Limit: limit}, offset)
	if err != nil {
		return nil, false, err
	}
	return page.Items, !page.Complete, nil
}

// ListPage shares the skill catalog's literal OR-within/AND-between filters.
// Offset supports older tool clients; cursors retain the shared snapshot/query
// binding. The immutable index is reused until SetCandidates changes metadata.
func (s *State) ListPage(ctx context.Context, query primitive.CatalogQuery, offset int) (*primitive.CatalogPage[Candidate], error) {
	if offset < 0 || query.Limit < 0 || offset != 0 && query.Cursor != "" {
		return nil, fmt.Errorf("invalid tool discovery bounds")
	}
	if query.Limit == 0 {
		query.Limit = 20
	}
	if query.Limit > 50 {
		query.Limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	index := s.catalog
	page, err := index.Page(ctx, query)
	if err != nil || offset == 0 {
		return page, err
	}
	// Keep the offset adapter on the shared catalog pager rather than adding
	// a second matcher or rebuilding an index for legacy requests.
	items := make([]Candidate, 0, query.Limit)
	remaining := offset
	for {
		start := remaining
		if start > len(page.Items) {
			start = len(page.Items)
		}
		remaining -= start
		count := query.Limit - len(items)
		if count > len(page.Items)-start {
			count = len(page.Items) - start
		}
		items = append(items, page.Items[start:start+count]...)
		if len(items) == query.Limit || page.Complete {
			break
		}
		query.Cursor = page.NextCursor
		page, err = index.Page(ctx, query)
		if err != nil {
			return nil, err
		}
	}
	// Legacy clients continue using nextOffset; a partial catalog page cannot
	// be represented by a cursor without changing its bound query.
	return &primitive.CatalogPage[Candidate]{Items: items, Total: page.Total, Complete: offset+len(items) >= page.Total}, nil
}

// Activate validates the complete exact-name batch before changing exposure.
func (s *State) Activate(names []string) error {
	if len(names) == 0 || len(names) > 50 {
		return fmt.Errorf("activate requires between 1 and 50 exact tool names")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range names {
		if !s.allowed[name] {
			return fmt.Errorf("tool is not an authorized candidate: %q", name)
		}
	}
	for _, name := range names {
		s.selected[name] = true
	}
	return nil
}

func (s *State) SelectedNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.selected))
	for name := range s.selected {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
