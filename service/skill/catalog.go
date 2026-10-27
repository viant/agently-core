package skill

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	primitive "github.com/viant/agently-core/protocol/primitive"
	skillproto "github.com/viant/agently-core/protocol/skill"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

var partialCatalogSequence atomic.Uint64

var errPartialSkillCatalog = errors.New("partial skill catalog")

type catalogOwner interface {
	CheckVisibleCatalog(context.Context, *primitive.Catalog[json.RawMessage]) error
	CatalogScope(context.Context, string, []string) (string, bool, error)
	VisibleCatalog(context.Context, string, time.Time, func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error), ...func() time.Time) (*primitive.Catalog[json.RawMessage], error)
}

// ListVisiblePage adapts existing native and ordinary MCP metadata discovery
// to the same manager-owned immutable index used by all primitive kinds.
// Activation/Get still resolves the current manifest and reads bytes freshly.
func (s *Service) ListVisiblePage(ctx context.Context, agent *agentmdl.Agent, query primitive.CatalogQuery) (*primitive.CatalogPage[skillproto.Metadata], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, discovery := withDiscoveryDiagnostics(ctx)
	names := []string{}
	if s.mcpSource != nil && authctx.EffectiveUserID(ctx) != "" {
		available, err := s.mcpSource.Names(ctx)
		if err != nil {
			traceDiscoveryFailure(ctx, "page_names", "", false, "", err)
			return nil, err
		}
		for _, name := range available {
			cfg, err := s.mcpSource.Options(ctx, name)
			if err != nil {
				traceDiscoveryFailure(ctx, "page_options", name, false, "", err)
				return nil, err
			}
			cfg = ordinarySkillDiscovery(cfg)
			if cfg != nil && (cfg.SkillDiscovery != nil && cfg.SkillDiscovery.Enabled || s.delivery != nil && cfg.PrimitiveProviderIdentity != "") {
				names = append(names, name)
			}
		}
	}
	scope := ""
	cacheable := false
	owner, hasOwner := s.mcpSource.(catalogOwner)
	conversation := requestctx.ConversationIDFromContext(ctx)
	if conversation == "" {
		conversation = "skill-catalog"
	}
	if hasOwner {
		var err error
		scope, cacheable, err = owner.CatalogScope(ctx, conversation, names)
		if err != nil {
			traceDiscoveryFailure(ctx, "page_scope", "", false, "", err)
			return nil, err
		}
	}
	s.mu.RLock()
	registry := s.registry
	s.mu.RUnlock()
	patterns := []string(nil)
	if agent != nil {
		patterns = agent.Skills
	}
	raw, _ := json.Marshal(patterns)
	fingerprint := sha256.Sum256(raw)
	key := fmt.Sprintf("skill:%p:%p:%x:%s", s, registry, fingerprint, scope)
	var partialItems []primitive.CatalogItem[json.RawMessage]
	load := func(ctx context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
		metadata, err := s.ListVisible(ctx, agent)
		if err != nil {
			return nil, err
		}
		items := make([]primitive.CatalogItem[json.RawMessage], 0, len(metadata))
		for _, entry := range metadata {
			namespace := skillCatalogNamespace(entry)
			raw, err := json.Marshal(entry)
			if err != nil {
				return nil, err
			}
			items = append(items, primitive.CatalogItem[json.RawMessage]{Name: entry.Name, Description: entry.Description, Namespace: namespace, URI: entry.URI, Provider: entry.ServerID, Value: raw})
		}
		if hasOwner && len(discovery.messages) > 0 {
			partialItems = items
			return nil, errPartialSkillCatalog
		}
		return items, nil
	}
	var index *primitive.Catalog[json.RawMessage]
	if hasOwner {
		until := time.Now().Add(30 * time.Second)
		if !cacheable {
			until = time.Now()
		}
		var err error
		index, err = owner.VisibleCatalog(ctx, key, until, load)
		if err == errPartialSkillCatalog {
			// Keep the owner's publication/authentication fences while avoiding
			// reuse of a partial snapshot on a subsequent request. Complete
			// catalogs retain the normal shared cache and its stable cursors.
			partialKey := fmt.Sprintf("%s:partial:%d", key, partialCatalogSequence.Add(1))
			index, err = owner.VisibleCatalog(ctx, partialKey, time.Now().Add(30*time.Second), func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
				return partialItems, nil
			})
		}
		if err != nil {
			return nil, err
		}
		current, _, err := owner.CatalogScope(ctx, conversation, names)
		if err != nil {
			return nil, err
		}
		if current != scope {
			return nil, primitive.ErrCatalogCursor
		}
	} else {
		items, err := load(ctx)
		if err != nil {
			return nil, err
		}
		encoded, _ := json.Marshal(items)
		digest := sha256.Sum256(encoded)
		identity, _ := json.Marshal([]string{key, authctx.EffectiveUserID(ctx), authctx.Provider(ctx), authctx.MCPAuthToken(ctx, false), authctx.MCPAuthToken(ctx, true), conversation})
		index = primitive.NewCatalog(fmt.Sprintf("uncached:%x:%x", sha256.Sum256(identity), digest), items)
	}
	page, err := index.Page(ctx, query)
	if err != nil {
		return nil, err
	}
	result := &primitive.CatalogPage[skillproto.Metadata]{NextCursor: page.NextCursor, Complete: page.Complete, Total: page.Total, Items: make([]skillproto.Metadata, 0, len(page.Items))}
	for _, raw := range page.Items {
		var item skillproto.Metadata
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		result.Items = append(result.Items, item)
	}
	if hasOwner {
		if err = owner.CheckVisibleCatalog(ctx, index); err != nil {
			return nil, err
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if len(discovery.messages) > 0 {
		// A new request must refresh provider availability; partial snapshot
		// cursors cannot safely resume against a different provider set.
		result.NextCursor = ""
		result.Complete = false
	}
	return result, nil
}
func (s *Service) ListPageForSelection(ctx context.Context, conversationID, agentID string, query primitive.CatalogQuery) (*primitive.CatalogPage[skillproto.Metadata], []string, error) {
	if agentID == "" {
		return s.ListPageForConversation(ctx, conversationID, query)
	}
	if s.agentFinder == nil {
		return nil, nil, fmt.Errorf("agent catalog unavailable")
	}
	agent, err := s.agentFinder.Find(ctx, agentID)
	if err != nil {
		return nil, nil, err
	}
	if agent == nil {
		return nil, nil, fmt.Errorf("agent not found")
	}
	if conversationID != "" {
		ctx = requestctx.WithConversationID(ctx, conversationID)
	}
	ctx, discovery := withDiscoveryDiagnostics(ctx)
	page, err := s.ListVisiblePage(ctx, agent, query)
	return page, append(s.Diagnostics(), discovery.messages...), err
}
func (s *Service) ListPageForConversation(ctx context.Context, conversationID string, query primitive.CatalogQuery) (*primitive.CatalogPage[skillproto.Metadata], []string, error) {
	if conversationID == "" {
		return nil, nil, fmt.Errorf("conversation ID is required")
	}
	ctx = requestctx.WithConversationID(ctx, conversationID)
	agent, err := s.currentAgent(ctx)
	if err != nil {
		return nil, nil, err
	}
	ctx, discovery := withDiscoveryDiagnostics(ctx)
	page, err := s.ListVisiblePage(ctx, agent, query)
	return page, append(s.Diagnostics(), discovery.messages...), err
}

// Namespace is the authored upstream skill namespace, not its MCP connection.
// Catalog identities retain their exact encoded resource URI and revision.
// Legacy non-skill resource URIs have no canonical namespace, so their existing
// ServerID is the fallback partition; no namespace is invented for local names.
func skillCatalogNamespace(entry skillproto.Metadata) string {
	if entry.Namespace != "" {
		return entry.Namespace
	}
	if u, err := url.Parse(entry.URI); err == nil && u.Scheme == "skill" && u.Host != "" && u.User == nil && strings.HasSuffix(u.Path, "/SKILL.md") && !strings.Contains(u.Path, "~") {
		return u.Host
	}
	if ref, err := skillproto.ParseRef(entry.URI); err == nil && ref.ResourceURI != "" {
		if upstream, err := url.Parse(ref.ResourceURI); err == nil && upstream.Scheme == "skill" && upstream.Host != "" && upstream.User == nil && strings.HasSuffix(upstream.Path, "/SKILL.md") {
			return upstream.Host
		}
	}
	return entry.ServerID
}
