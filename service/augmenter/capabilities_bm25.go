package augmenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/runtime/capability"
	"github.com/viant/embedius/bm25"
)

type lexicalCapabilities struct {
	index    *bm25.Index
	byID     map[string]capability.Candidate
	exact    map[string][]string
	retained []string
}

func (s *Service) rankLexicalCapabilities(ctx context.Context, request CapabilityRequest, limit int) ([]capability.Candidate, error) {
	// Reuse the existing private scope and credential fences. Metadata is already
	// authorized by the caller; ranking does not grant execution permissions.
	scope, err := capabilityEmbeddingScope(ctx, "bm25:v1", "query")
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	if err = encoder.Encode(scope); err != nil {
		return nil, err
	}
	if err = encoder.Encode(request.NamespaceDescriptions); err != nil {
		return nil, err
	}
	// This request API currently supplies metadata rather than a generation handle.
	// Hash its metadata (never schemas) to preserve existing content invalidation.
	for _, c := range request.Candidates {
		if err = encoder.Encode([]any{c.ID(), c.Description, c.Namespace, c.CacheScope, c.Retained, c.LookupNames}); err != nil {
			return nil, err
		}
	}
	var key [32]byte
	copy(key[:], hash.Sum(nil))
	pending := s.capabilityLoads.DoChan(fmt.Sprintf("bm25:%x", key), func() (any, error) {
		now := time.Now()
		s.capabilityVectors.mu.Lock()
		if held := s.capabilityVectors.entries[key]; held != nil && held.lexical != nil && (held.expires.IsZero() || held.expires.After(now)) {
			held.used = now
			result := held.lexical
			s.capabilityVectors.mu.Unlock()
			return result, nil
		}
		s.capabilityVectors.mu.Unlock()
		candidates := append([]capability.Candidate(nil), request.Candidates...)
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID() < candidates[j].ID() })
		compiled := &lexicalCapabilities{byID: map[string]capability.Candidate{}, exact: map[string][]string{}}
		documents := make([]bm25.Document, 0, len(candidates))
		size := 0
		for _, c := range candidates {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			id := c.ID()
			if _, ok := compiled.byID[id]; ok {
				continue
			}
			c.LookupNames = append([]string(nil), c.LookupNames...)
			compiled.byID[id] = c
			text := strings.Join([]string{c.Name, strings.Join(c.LookupNames, " "), c.Description, c.Namespace, request.NamespaceDescriptions[c.Namespace], c.URI}, " ")
			documents = append(documents, bm25.Document{ID: id, Text: text})
			size += len(text)*32 + 256
			if c.Retained {
				compiled.retained = append(compiled.retained, id)
			}
			for _, name := range append([]string{c.Name, c.URI}, c.LookupNames...) {
				if name == "" {
					continue
				}
				normal := strings.ToLower(name)
				compiled.exact[normal] = append(compiled.exact[normal], id)
				if c.Kind == "tool" {
					canonical := strings.ToLower(mcpname.Canonical(name))
					compiled.exact[canonical] = append(compiled.exact[canonical], id)
				}
			}
		}
		compiled.index, err = bm25.New(ctx, documents)
		if err != nil {
			return nil, err
		}
		if size <= 64<<20 {
			s.capabilityVectors.mu.Lock()
			if s.capabilityVectors.entries == nil {
				s.capabilityVectors.entries = map[[32]byte]*capabilityVectors{}
			}
			s.capabilityVectors.evict(now, size)
			s.capabilityVectors.entries[key] = &capabilityVectors{lexical: compiled, used: now, size: size}
			s.capabilityVectors.bytes += size
			s.capabilityVectors.mu.Unlock()
		}
		return compiled, nil
	})
	var compiled *lexicalCapabilities
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-pending:
		if result.Err != nil {
			return nil, result.Err
		}
		compiled = result.Val.(*lexicalCapabilities)
	}
	hits, err := compiled.index.Search(ctx, request.Query, limit)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, nil
	}
	selected := map[string]bool{}
	result := make([]capability.Candidate, 0, len(hits)+len(compiled.retained))
	add := func(id string) {
		if selected[id] {
			return
		}
		c, ok := compiled.byID[id]
		if !ok {
			return
		}
		selected[id] = true
		c.LookupNames = append([]string(nil), c.LookupNames...)
		result = append(result, c)
	}
	for _, hit := range hits {
		add(hit.ID)
	}
	for _, id := range compiled.retained {
		add(id)
	}
	for _, token := range strings.FieldsFunc(request.Query, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("\"'`()[]{}<>,;", r) }) {
		token = strings.ToLower(strings.TrimSuffix(token, "."))
		for _, id := range compiled.exact[token] {
			add(id)
		}
		for _, id := range compiled.exact[strings.ToLower(mcpname.Canonical(token))] {
			if candidate, ok := compiled.byID[id]; ok && candidate.Kind == "tool" && exactCapabilityReference(request.Query, candidate) {
				add(id)
			}
		}
	}
	return result, ctx.Err()
}
