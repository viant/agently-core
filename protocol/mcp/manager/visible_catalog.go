package manager

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	primitive "github.com/viant/agently-core/protocol/primitive"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

type visibleCatalog struct {
	ready       chan struct{}
	index       *primitive.Catalog[json.RawMessage]
	until       time.Time
	sourceUntil time.Time
}

// CatalogScope keeps visible metadata private, regardless of tools/list's
// visibility. It reuses current configured/authenticated connection identity.
func (m *Manager) CatalogScope(ctx context.Context, conversation string, names []string) (string, bool, error) {
	scope := []string{m.poolKey(ctx, conversation), authctx.EffectiveUserID(ctx), authctx.CanonicalUserID(ctx), requestctx.ConversationIDFromContext(ctx), authctx.Provider(ctx), authctx.Bearer(ctx), authctx.IDToken(ctx), authctx.MCPAuthToken(ctx, false), authctx.MCPAuthToken(ctx, true)}
	names = append([]string(nil), names...)
	sort.Strings(names)
	cacheable := true
	for _, name := range names {
		if !m.IsLocalClient(name) && (m.cookieJar != nil || m.jarProvider != nil || m.authRT != nil || m.authRTProvider != nil) {
			cacheable = false
		}
		cfg, err := m.Options(ctx, name)
		if err != nil {
			return "", false, err
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			return "", false, err
		}
		effective := m.WithAuthTokenContext(ctx, name)
		cli, err := m.Get(effective, conversation, name)
		if err != nil {
			return "", false, err
		}
		transportToken, _ := effective.Value(authtransport.ContextAuthTokenKey).(string)
		credential := ""
		if cfg != nil && cfg.ClientOptions != nil {
			if cfg.CookieJar != nil {
				cacheable = false
			}
			if cfg.IsDelegatedAuth() {
				if m.credResolver == nil {
					return "", false, fmt.Errorf("catalog credential resolver unavailable")
				}
				requirement, err := cfg.Auth.CompileRequirement(effective, name, cfg.Transport.URL)
				if err != nil {
					return "", false, err
				}
				resolved, err := m.credResolver.Resolve(effective, *requirement)
				if err != nil {
					return "", false, err
				}
				if err = resolved.Verify(requirement, time.Now()); err != nil {
					return "", false, err
				}
				credential = resolved.Token
			}
		}
		m.mu.Lock()
		generation := m.toolCatalogGeneration[name]
		m.mu.Unlock()
		scope = append(scope, fmt.Sprintf("%s:%s:%p:%d:%s:%s", name, raw, cli, generation, transportToken, credential))
	}
	raw, _ := json.Marshal(scope)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum), cacheable, nil
}

// VisibleCatalog is the one manager-owned snapshot store used by primitive
// and native/ordinary skill adapters. A snapshot is metadata, never authority.
func (m *Manager) VisibleCatalog(ctx context.Context, key string, until time.Time, load func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error), sourceLease ...func() time.Time) (*primitive.Catalog[json.RawMessage], error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m.mu.Lock()
		if m.visibleCatalogs == nil {
			m.visibleCatalogs = map[string]*visibleCatalog{}
		}
		now := time.Now()
		if held := m.visibleCatalogs[key]; held != nil {
			if held.ready != nil {
				ready := held.ready
				m.mu.Unlock()
				select {
				case <-ready:
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if held.until.After(now) {
				index := held.index
				m.mu.Unlock()
				return index, nil
			}
			delete(m.visibleCatalogs, key)
		}
		for k, v := range m.visibleCatalogs {
			if v.ready == nil && !v.until.After(now) {
				delete(m.visibleCatalogs, k)
			}
		}
		if len(m.visibleCatalogs) >= 128 {
			m.mu.Unlock()
			return nil, fmt.Errorf("visible catalog cache capacity exceeded")
		}
		if !until.After(now) {
			until = time.Time{}
		}
		pending := &visibleCatalog{ready: make(chan struct{})}
		m.visibleCatalogs[key] = pending
		if m.catalogSequence == 0 {
			if _, err := rand.Read(m.catalogEpoch[:]); err != nil {
				delete(m.visibleCatalogs, key)
				m.mu.Unlock()
				return nil, err
			}
		}
		m.catalogSequence++
		snapshot := fmt.Sprintf("%s:%x:%d", key, m.catalogEpoch, m.catalogSequence)
		m.mu.Unlock()
		items, err := load(ctx)
		var sourceUntil time.Time
		if err == nil && len(sourceLease) > 0 && sourceLease[0] != nil {
			sourceUntil = sourceLease[0]()
			if !sourceUntil.IsZero() {
				if !sourceUntil.After(time.Now()) {
					err = primitive.ErrCatalogCursor
				}
				if !until.IsZero() && sourceUntil.Before(until) {
					until = sourceUntil
				}
			}
		}
		var index *primitive.Catalog[json.RawMessage]
		if err == nil && ctx.Err() == nil {
			index = primitive.NewCatalog(snapshot, items)
		}
		m.mu.Lock()
		if m.visibleCatalogs[key] != pending && err == nil {
			err = primitive.ErrCatalogCursor
		}
		if m.visibleCatalogs[key] == pending {
			if err == nil && ctx.Err() == nil {
				pending.index = index
				pending.until = until
				pending.sourceUntil = sourceUntil
			} else {
				delete(m.visibleCatalogs, key)
			}
		}
		close(pending.ready)
		pending.ready = nil
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return index, nil
	}
}

// CheckVisibleCatalog linearizes delivery against invalidation and eviction.
func (m *Manager) CheckVisibleCatalog(ctx context.Context, index *primitive.Catalog[json.RawMessage]) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, held := range m.visibleCatalogs {
		if held.ready == nil && held.index == index && (held.until.IsZero() || held.until.After(time.Now())) && (held.sourceUntil.IsZero() || held.sourceUntil.After(time.Now())) {
			return nil
		}
	}
	return primitive.ErrCatalogCursor
}
