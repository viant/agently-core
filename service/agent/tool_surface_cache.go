package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/logx"
	toolctx "github.com/viant/agently-core/protocol/tool"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

type catalogSurfaceEntry struct {
	definitions []*llm.ToolDefinition
	expiresAt   time.Time
}

var catalogSurfacePublications atomic.Uint64

func cloneCatalogSurface(definitions []*llm.ToolDefinition) []*llm.ToolDefinition {
	result := make([]*llm.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if definition != nil {
			copy := cloneToolDefinition(*definition)
			result = append(result, &copy)
		}
	}
	return result
}

func loadCatalogSurface(cache *sync.Map, key string) ([]*llm.ToolDefinition, bool) {
	if key == "" {
		return nil, false
	}
	value, ok := cache.Load(key)
	if !ok {
		return nil, false
	}
	entry := value.(*catalogSurfaceEntry)
	if !entry.expiresAt.After(time.Now()) {
		cache.CompareAndDelete(key, value)
		return nil, false
	}
	return cloneCatalogSurface(entry.definitions), true
}

func storeCatalogSurface(cache *sync.Map, key string, definitions []*llm.ToolDefinition) {
	if key == "" {
		return
	}
	// Sweep expired generations on publication to bound stale catalog entries.
	now := time.Now()
	if catalogSurfacePublications.Add(1)%64 == 0 {
		cache.Range(func(key, value interface{}) bool {
			if !value.(*catalogSurfaceEntry).expiresAt.After(now) {
				cache.CompareAndDelete(key, value)
			}
			return true
		})
	}
	cache.Store(key, &catalogSurfaceEntry{cloneCatalogSurface(definitions), now.Add(5 * time.Minute)})
}

func (s *Service) cachedBackendToolDefinitions(ctx context.Context, pattern string) ([]*llm.ToolDefinition, error) {
	identity, supportsCache := s.registry.(interface {
		ToolSurfaceCacheIdentity(context.Context, string) (string, string)
	})
	var cache *sync.Map
	key := ""
	initialGeneration := ""
	if supportsCache {
		kind, generation := identity.ToolSurfaceCacheIdentity(ctx, pattern)
		if generation != "" {
			initialGeneration = generation
			key = pattern + "\x1e" + generation
			if kind == "public" {
				cache = &s.publicToolSurfaceCache
			}
			if kind == "private" {
				cache = &s.privateToolSurfaceCache
			}
		}
	}
	if cache != nil {
		if definitions, ok := loadCatalogSurface(cache, key); ok {
			return definitions, nil
		}
	}
	var definitions []*llm.ToolDefinition
	var err error
	if matcher, ok := s.registry.(toolctx.ContextMatcherWithError); ok {
		definitions, err = matcher.MatchDefinitionWithContextResult(ctx, pattern)
	} else if matcher, ok := s.registry.(toolctx.ContextMatcher); ok {
		definitions = matcher.MatchDefinitionWithContext(ctx, pattern)
	} else {
		definitions = s.registry.MatchDefinition(pattern)
	}
	if err != nil {
		turn, _ := requestctx.TurnMetaFromContext(ctx)
		code, status := diagnosticRPCFields(err)
		logx.OperationalWarnf("conversation", "tool.catalog.match.failed convo=%q turn=%q pattern=%q errorType=%T rpcCode=%d rpcStatus=%d", requestctx.ConversationIDFromContext(ctx), turn.TurnID, diagnosticToolPattern(pattern), err, code, status)
	}
	if err == nil && ctx.Err() == nil && supportsCache {
		// First authenticated discovery may create the principal catalog.
		kind, generation := identity.ToolSurfaceCacheIdentity(ctx, pattern)
		if initialGeneration != "" && generation != initialGeneration {
			return definitions, err
		}
		if generation != "" {
			key = pattern + "\x1e" + generation
			if kind == "public" {
				storeCatalogSurface(&s.publicToolSurfaceCache, key, definitions)
			}
			if kind == "private" {
				storeCatalogSurface(&s.privateToolSurfaceCache, key, definitions)
			}
		}
	}
	return definitions, err
}

func (s *Service) cachedBrowserToolDefinitions(ctx context.Context, server, pattern string, matched []*llm.ToolDefinition) []*llm.ToolDefinition {
	// Only validated browser definitions enter this path. Their metadata carries
	// the catalog and originating connection identity; add explicit owner/thread.
	matched = cloneCatalogSurface(matched)
	sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })
	raw, err := json.Marshal([]interface{}{authctx.EffectiveUserID(ctx), requestctx.ConversationIDFromContext(ctx), server, pattern, matched})
	if err != nil {
		return matched
	}
	key := fmt.Sprintf("%x", sha256.Sum256(raw))
	if definitions, ok := loadCatalogSurface(&s.browserToolSurfaceCache, key); ok {
		return definitions
	}
	storeCatalogSurface(&s.browserToolSurfaceCache, key, matched)
	return matched
}
