package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/protocol/primitive/observe"
	identity "github.com/viant/agently-core/protocol/resource"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk/api"
	authctx "github.com/viant/agently-core/service/auth"
	"github.com/viant/forge/backend/types"
)

// PublishedWindowOpen reopens an admitted immutable selected variant locally.
// The replica owns policy, session and generation freshness; no MCP metadata
// request or legacy ResolvedResource lease is synthesized by this adapter.
type PublishedWindowOpen func(context.Context, string, string, *types.WindowTarget) (*types.WindowResourceVariant, string, error)
type PublishedDatasourceExecute func(context.Context, *dsproto.DataSource, map[string]interface{}, map[string]any) (*api.FetchDatasourceOutput, error)
type VerifiedWindowOwner func(context.Context) (string, error)
type publishedWindowBinding struct {
	clientID, provider, uri, hash, owner, session string
	target                                        *types.WindowTarget
	descriptors                                   map[string]json.RawMessage
}
type publishedBindingKey struct{ owner, session, windowID string }
type PublishedWindowBindings struct {
	mu                 sync.RWMutex
	entries            map[publishedBindingKey]publishedWindowBinding
	protectedProviders map[string]bool
	open               PublishedWindowOpen
	execute            PublishedDatasourceExecute
	owner              VerifiedWindowOwner
}

func defaultVerifiedWindowOwner(ctx context.Context) (string, error) {
	owner, err := authctx.LocalStateOwner(ctx)
	if err != nil || owner == nil || owner.Binding == "" {
		return "", identity.ErrResourceDenied
	}
	return owner.Binding, nil
}
func NewPublishedWindowBindings(open PublishedWindowOpen, execute PublishedDatasourceExecute, owner VerifiedWindowOwner) *PublishedWindowBindings {
	if owner == nil {
		owner = defaultVerifiedWindowOwner
	}
	return &PublishedWindowBindings{entries: map[publishedBindingKey]publishedWindowBinding{}, protectedProviders: map[string]bool{}, open: open, execute: execute, owner: owner}
}

// ProtectProvider is host-only setup, called before serving UI requests. It
// also covers a formerly published root after its current grant is removed.
func (b *PublishedWindowBindings) ProtectProvider(providerIdentity string) {
	if b == nil || providerIdentity == "" {
		return
	}
	b.mu.Lock()
	if b.protectedProviders == nil {
		b.protectedProviders = map[string]bool{}
	}
	b.protectedProviders[providerIdentity] = true
	b.mu.Unlock()
}

func (b *PublishedWindowBindings) protectedProviderIdentities() []string {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	providers := make([]string, 0, len(b.protectedProviders))
	for provider := range b.protectedProviders {
		providers = append(providers, provider)
	}
	b.mu.RUnlock()
	return providers
}
func bindingKey(owner, session, windowID string) publishedBindingKey {
	return publishedBindingKey{owner: owner, session: session, windowID: windowID}
}
func cloneTarget(target *types.WindowTarget) *types.WindowTarget {
	if target == nil {
		return nil
	}
	raw, _ := json.Marshal(target)
	var copy types.WindowTarget
	if json.Unmarshal(raw, &copy) != nil {
		return nil
	}
	return &copy
}

// Bind runs only from the trusted UI handler after a successful selected
// Replica.OpenWindowVariant. Browser JSON never populates this registry.
func (b *PublishedWindowBindings) Bind(ctx context.Context, clientID, windowID, providerIdentity, uri, hash string, variant *types.WindowResourceVariant) error {
	if b == nil || b.open == nil || b.execute == nil || variant == nil || variant.Window == nil || strings.TrimSpace(clientID) == "" || strings.TrimSpace(windowID) == "" || strings.TrimSpace(providerIdentity) == "" || strings.TrimSpace(uri) == "" || strings.TrimSpace(hash) == "" {
		return identity.ErrResourceDenied
	}
	owner, err := b.owner(ctx)
	if err != nil || owner == "" {
		return identity.ErrResourceDenied
	}
	session := runtimerequestctx.UIBridgeSessionIDFromContext(ctx)
	if session == "" {
		return identity.ErrResourceDenied
	}
	copied := make(map[string]json.RawMessage, len(variant.DataSources))
	for id, raw := range variant.DataSources {
		if id == "" || len(raw) == 0 || !json.Valid(raw) {
			return identity.ErrResourceDenied
		}
		copied[id] = bytes.Clone(raw)
	}
	entry := publishedWindowBinding{clientID: clientID, provider: providerIdentity, uri: uri, hash: hash, owner: owner, session: session, target: cloneTarget(variant.Window.ResourceTarget), descriptors: copied}
	b.mu.Lock()
	b.entries[bindingKey(owner, session, windowID)] = entry
	b.mu.Unlock()
	return nil
}
func (b *PublishedWindowBindings) lookup(ctx context.Context, windowID string) (publishedWindowBinding, bool, error) {
	if b == nil || windowID == "" {
		return publishedWindowBinding{}, false, nil
	}
	ownerSpan := observe.Start(ctx, "window.binding.owner", observe.Labels{})
	owner, err := b.owner(ctx)
	if err != nil || owner == "" {
		ownerSpan.End(observe.Denied, observe.CacheUnknown)
		return publishedWindowBinding{}, false, identity.ErrResourceDenied
	}
	ownerSpan.End(observe.OK, observe.CacheUnknown)
	session := runtimerequestctx.UIBridgeSessionIDFromContext(ctx)
	if session == "" {
		return publishedWindowBinding{}, false, identity.ErrResourceDenied
	}
	b.mu.RLock()
	entry, ok := b.entries[bindingKey(owner, session, windowID)]
	if !ok {
		// A binding held by another verified owner or bridge session is a
		// denial, not a restart-induced missing binding. Keep that distinction
		// before the HTTP handler offers one metadata readmission attempt.
		for key := range b.entries {
			if key.windowID == windowID {
				b.mu.RUnlock()
				return publishedWindowBinding{}, false, identity.ErrResourceDenied
			}
		}
	}
	b.mu.RUnlock()
	return entry, ok, nil
}

// Fetch reports handled=false only when there is no server-side cohort binding.
// A declared cohort request should treat that case as denial, never fallback.
func (b *PublishedWindowBindings) Fetch(ctx context.Context, windowID, id string, inputs map[string]interface{}, windowParameters map[string]any) (*api.FetchDatasourceOutput, bool, error) {
	entry, ok, err := b.lookup(ctx, windowID)
	if err != nil {
		return nil, true, err
	}
	if !ok {
		return nil, false, nil
	}
	if _, declared := entry.descriptors[id]; !declared {
		return nil, true, identity.ErrResourceDenied
	}
	current, hash, err := b.open(ctx, entry.provider, entry.uri, cloneTarget(entry.target))
	if err != nil || current == nil || hash != entry.hash {
		return nil, true, identity.ErrResourceDenied
	}
	raw := current.DataSources[id]
	if len(raw) == 0 || !bytes.Equal(raw, entry.descriptors[id]) {
		return nil, true, identity.ErrResourceDenied
	}
	var descriptor dsproto.DataSource
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&descriptor); err != nil || descriptor.ID != id || descriptor.Backend == nil {
		return nil, true, identity.ErrResourceDenied
	}
	out, err := b.execute(ctx, &descriptor, inputs, windowParameters)
	if err != nil {
		return nil, true, err
	}
	fresh, freshHash, err := b.open(ctx, entry.provider, entry.uri, cloneTarget(entry.target))
	if err != nil || fresh == nil || freshHash != entry.hash || !bytes.Equal(fresh.DataSources[id], raw) {
		return nil, true, identity.ErrResourceDenied
	}
	if owner, err := b.owner(ctx); err != nil || owner != entry.owner || runtimerequestctx.UIBridgeSessionIDFromContext(ctx) != entry.session {
		return nil, true, identity.ErrResourceDenied
	}
	return out, true, nil
}

// Forget removes only the exact closed version, so a delayed close cannot
// erase a newer binding for the same browser window ID.
func (b *PublishedWindowBindings) Forget(ctx context.Context, windowID, expectedHash string) error {
	entry, ok, err := b.lookup(ctx, windowID)
	if err != nil || !ok {
		return err
	}
	owner, _ := b.owner(ctx)
	session := runtimerequestctx.UIBridgeSessionIDFromContext(ctx)
	b.mu.Lock()
	if current, held := b.entries[bindingKey(owner, session, windowID)]; held && current.hash == expectedHash && entry.hash == expectedHash {
		delete(b.entries, bindingKey(owner, session, windowID))
	}
	b.mu.Unlock()
	return nil
}
