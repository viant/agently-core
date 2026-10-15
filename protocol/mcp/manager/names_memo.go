package manager

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"sync"

	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/runtime/requestctx"
)

// namesMemo is scoped to one inbound operation. It avoids rebuilding the
// dynamic MCP connection inventory for each nested primitive lookup while
// preserving a fresh owner-supplied list for the next operation.
type namesMemo struct {
	mu          sync.Mutex
	values      map[namesMemoEntry][]string
	definitions map[definitionMemoEntry]definitionMemoValue
}

type namesMemoContextKey struct{}
type namesMemoEntry struct {
	manager           *Manager
	scope, generation string
}
type definitionMemoEntry struct {
	provider                *RepoProvider
	name, scope, generation string
}
type definitionMemoValue struct {
	raw      []byte
	identity *mcpcfg.ResolvedServiceIdentity
}

// WithNamesMemo establishes a detached, request-local inventory snapshot.
// The manager still checks effective actor and credential scope on each read.
func WithNamesMemo(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, namesMemoContextKey{}, &namesMemo{values: map[namesMemoEntry][]string{}, definitions: map[definitionMemoEntry]definitionMemoValue{}})
}

func namesMemoFromContext(ctx context.Context) *namesMemo {
	if ctx == nil {
		return nil
	}
	memo, _ := ctx.Value(namesMemoContextKey{}).(*namesMemo)
	return memo
}

func (memo *namesMemo) get(manager *Manager, scope, generation string) ([]string, bool) {
	memo.mu.Lock()
	defer memo.mu.Unlock()
	value, ok := memo.values[namesMemoEntry{manager: manager, scope: scope, generation: generation}]
	return append([]string(nil), value...), ok
}

func (memo *namesMemo) put(manager *Manager, scope, generation string, value []string) {
	memo.mu.Lock()
	defer memo.mu.Unlock()
	memo.values[namesMemoEntry{manager: manager, scope: scope, generation: generation}] = append([]string(nil), value...)
}

func definitionMemoScope(ctx context.Context) string {
	raw, _ := json.Marshal([]any{
		authctx.User(ctx), authctx.CanonicalUserID(ctx), authctx.EffectiveUserID(ctx),
		authctx.Provider(ctx), authctx.Bearer(ctx), authctx.IDToken(ctx),
		authctx.MCPAuthToken(ctx, false), authctx.MCPAuthToken(ctx, true),
		requestctx.ConversationIDFromContext(ctx),
	})
	sum := sha256.Sum256(raw)
	return string(sum[:])
}

func (memo *namesMemo) getDefinition(key definitionMemoEntry) (definitionMemoValue, bool) {
	memo.mu.Lock()
	defer memo.mu.Unlock()
	value, ok := memo.definitions[key]
	value.raw = append([]byte(nil), value.raw...)
	if value.identity != nil {
		copy := *value.identity
		value.identity = &copy
	}
	return value, ok
}

func (memo *namesMemo) putDefinition(key definitionMemoEntry, value definitionMemoValue) {
	memo.mu.Lock()
	defer memo.mu.Unlock()
	value.raw = append([]byte(nil), value.raw...)
	if value.identity != nil {
		copy := *value.identity
		value.identity = &copy
	}
	memo.definitions[key] = value
}

// WithRequestCatalog shares configuration/catalog preparation within one HTTP
// operation. It never persists selected resource bytes or tool-call results.
func WithRequestCatalog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithToolCatalogReadSnapshot(WithNamesMemo(r.Context()))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
