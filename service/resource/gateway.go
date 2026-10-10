// Package resource federates delegated primitive providers. A gateway never
// exports its aggregate catalog as a local authoritative provider.
package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	producer "github.com/viant/agently-core/service/primitiveproducer"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrCollision = errors.New("primitive provider identity collision")
var ErrUnavailable = errors.New("primitive provider unavailable")

type ActorResolver = producer.ActorResolver
type ActorVerifier = producer.ActorVerifier

// Connection is a transport locator, separate from canonical resource identity.
type Connection struct {
	Name             string `json:"name"`
	ProviderIdentity string `json:"providerIdentity"`
	Generation       uint64 `json:"generation"`
}
type LocatedResource struct {
	Connection Connection              `json:"connection"`
	Resource   primitive.ResourceState `json:"resource"`
}
type capabilities struct {
	actor      identity.VerifiedActor
	connection Connection
	namespaces []primitive.NamespaceCapabilities
	installed  map[string]bool
	expires    time.Time
}
type Gateway struct {
	Manager                 *manager.Manager
	Actor                   ActorResolver
	Verify                  ActorVerifier
	GatewayProviderIdentity string
	Now                     func() time.Time
	mu                      sync.Mutex
	generations             map[string]uint64
	cache                   map[string]capabilities
	stopChanges             func()
	closed                  bool
	observedOwners          map[string]string
}

func NewGateway(m *manager.Manager, a ActorResolver, v ActorVerifier, localIdentity string) *Gateway {
	g := &Gateway{Manager: m, Actor: a, Verify: v, GatewayProviderIdentity: localIdentity, generations: map[string]uint64{}, cache: map[string]capabilities{}, observedOwners: map[string]string{}}
	if m != nil {
		g.stopChanges = m.OnCatalogChange(g.Invalidate)
	}
	return g
}
func (g *Gateway) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}
func (g *Gateway) Close() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.closed = true
	g.cache = map[string]capabilities{}
	stop := g.stopChanges
	g.stopChanges = nil
	g.mu.Unlock()
	if stop != nil {
		stop()
	}
}
func (g *Gateway) Invalidate(connection string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.generations[connection]++
	for key, c := range g.cache {
		if c.connection.Name == connection {
			delete(g.cache, key)
		}
	}
}
func (g *Gateway) generation(name string) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.generations[name]
}
func (g *Gateway) actor(ctx context.Context) (identity.VerifiedActor, error) {
	if g == nil || g.Manager == nil || g.Actor == nil || g.Verify == nil {
		return identity.VerifiedActor{}, identity.ErrResourceDenied
	}
	g.mu.Lock()
	closed := g.closed
	g.mu.Unlock()
	if closed {
		return identity.VerifiedActor{}, identity.ErrResourceDenied
	}
	var scopeErr error
	ctx, scopeErr = ReadContext(ctx)
	if scopeErr != nil {
		return identity.VerifiedActor{}, scopeErr
	}
	a, e := g.Actor(ctx)
	if e != nil {
		return a, e
	}
	if !a.Valid(g.now()) || ctx.Err() != nil {
		return a, identity.ErrResourceDenied
	}
	if e = g.Verify(ctx, a); e != nil {
		return a, e
	}
	if sequence := sequenceFromContext(ctx); sequence != nil {
		if sequence.gateway != g {
			return a, identity.ErrResourceDenied
		}
		if e = sequence.checkActor(a); e != nil {
			return a, e
		}
	}
	return a, nil
}
func (g *Gateway) final(ctx context.Context, a identity.VerifiedActor) error {
	g.mu.Lock()
	closed := g.closed
	g.mu.Unlock()
	if closed {
		return identity.ErrResourceDenied
	}
	var scopeErr error
	ctx, scopeErr = ReadContext(ctx)
	if scopeErr != nil {
		return scopeErr
	}
	if sequence := sequenceFromContext(ctx); sequence != nil {
		if sequence.gateway != g {
			return identity.ErrResourceDenied
		}
		if err := sequence.checkActor(a); err != nil {
			return err
		}
	}
	if !a.Valid(g.now()) || ctx.Err() != nil {
		return identity.ErrResourceDenied
	}
	return g.Verify(ctx, a)
}
func actorKey(a identity.VerifiedActor, name string, generation uint64) string {
	raw, _ := json.Marshal([]any{a.Issuer, a.Subject, a.TenantID, a.AccountID, a.IdentityRevision, name, generation})
	return string(raw)
}
func (g *Gateway) client(ctx context.Context, name string) (mcpclient.Interface, error) {
	client, err := g.Manager.Get(g.Manager.WithAuthTokenContext(ctx, name), "", name)
	if err != nil {
		return nil, err
	}
	return &gatewayAuthClient{Interface: client, manager: g.Manager, name: name}, nil
}
func toolRaw(ctx context.Context, c mcpclient.Interface, method string, in any) (json.RawMessage, error) {
	raw, e := json.Marshal(in)
	if e != nil {
		return nil, e
	}
	var args map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if e = decoder.Decode(&args); e != nil {
		return nil, e
	}
	result, e := c.CallTool(ctx, &schema.CallToolRequestParams{Name: method, Arguments: args}, mcpclient.WithNoRetry())
	if e != nil {
		return nil, e
	}
	if result == nil || result.IsError != nil && *result.IsError {
		return nil, identity.ErrResourceDenied
	}
	if result.StructuredContent != nil {
		return json.Marshal(result.StructuredContent)
	}
	for _, content := range result.Content {
		if text, ok := content.(schema.TextContent); ok && json.Valid([]byte(text.Text)) {
			return json.RawMessage(text.Text), nil
		}
		if object, ok := content.(map[string]any); ok {
			kind, _ := object["type"].(string)
			text, _ := object["text"].(string)
			if kind == "text" && json.Valid([]byte(text)) {
				return json.RawMessage(text), nil
			}
		}
	}
	return nil, identity.ErrResource
}
func call[T any](ctx context.Context, c mcpclient.Interface, method string, in any) (T, error) {
	var out T
	raw, e := toolRaw(ctx, c, method, in)
	if e != nil {
		return out, e
	}
	e = json.Unmarshal(raw, &out)
	return out, e
}

func (g *Gateway) discover(ctx context.Context, a identity.VerifiedActor, name string, binding ...*resourceReadBinding) (capabilities, error) {
	return g.discoverMode(ctx, a, name, false, binding...)
}

func (g *Gateway) discoverMode(ctx context.Context, a identity.VerifiedActor, name string, force bool, bindings ...*resourceReadBinding) (capabilities, error) {
	var scopeErr error
	ctx, scopeErr = ReadContext(ctx)
	if scopeErr != nil {
		return capabilities{}, scopeErr
	}
	var binding *resourceReadBinding
	if len(bindings) > 0 {
		binding = bindings[0]
	}
	var options *cfg.MCPClient
	var c mcpclient.Interface
	var e error
	if binding != nil {
		if binding.manager != g.Manager || binding.name != name {
			return capabilities{}, identity.ErrResourceDenied
		}
		options, c = binding.options, binding.client
	} else {
		options, e = g.Manager.Options(ctx, name)
		if e != nil {
			return capabilities{}, e
		}
		if options == nil {
			return capabilities{}, ErrUnavailable
		}
		c, e = g.client(ctx, name)
		if e != nil {
			return capabilities{}, e
		}
	}
	restrictions := append([]string(nil), options.PrimitiveNamespaces...)
	sort.Strings(restrictions)
	restrictionKey, _ := json.Marshal(restrictions)
	generation := g.generation(name)
	key := actorKey(a, name, generation) + string(restrictionKey) + options.PrimitiveProviderIdentity
	g.mu.Lock()
	cached, ok := g.cache[key]
	g.mu.Unlock()
	sequence := sequenceFromContext(ctx)
	readKey := ""
	if sequence != nil && !force {
		if binding != nil {
			readKey = binding.key
		} else {
			readKey, e = g.namespaceReadKey(ctx, a, name, options, c)
			if e != nil {
				return capabilities{}, e
			}
		}
		if read, found, err := sequence.lookup(name, readKey); found || err != nil {
			if err != nil {
				return capabilities{}, err
			}
			if err := g.final(ctx, a); err != nil {
				return capabilities{}, err
			}
			return read, nil
		}
	}
	installed := map[string]bool{}
	owners := map[string]bool{}
	if !force && ok && cached.expires.After(g.now()) {
		installed = cached.installed
		owners[cached.connection.ProviderIdentity] = true
	} else {
		toolCursor := (*string)(nil)
		for page := 0; page < 100; page++ {
			tools, e := c.ListTools(ctx, toolCursor)
			if e != nil {
				return capabilities{}, e
			}
			if tools == nil {
				return capabilities{}, ErrUnavailable
			}
			for _, tool := range tools.Tools {
				raw, _ := json.Marshal(tool.Meta[primitive.AuthoringExtension])
				var meta struct {
					Version          int    `json:"version"`
					ProviderIdentity string `json:"providerIdentity"`
					Transport        string `json:"transport"`
				}
				if json.Unmarshal(raw, &meta) == nil && meta.Version == 1 && meta.ProviderIdentity != "" && meta.Transport == "tools/call" {
					if installed[tool.Name] {
						return capabilities{}, identity.ErrResource
					}
					installed[tool.Name] = true
					owners[meta.ProviderIdentity] = true
				}
			}
			if tools.NextCursor == nil || *tools.NextCursor == "" {
				break
			}
			if page == 99 || toolCursor != nil && *toolCursor == *tools.NextCursor {
				return capabilities{}, identity.ErrResource
			}
			toolCursor = tools.NextCursor
		}

	}
	if !installed["namespaces/list"] || !installed["namespaces/get"] {
		return capabilities{}, ErrUnavailable
	}
	if len(owners) != 1 {
		return capabilities{}, ErrCollision
	}
	listedFirst, e := call[primitive.NamespaceListResult](ctx, c, "namespaces/list", primitive.NamespaceListRequest{Limit: 100})
	if e != nil {
		return capabilities{}, e
	}
	owner := listedFirst.ProviderIdentity
	if owner == "" || !owners[owner] || owner == g.GatewayProviderIdentity {
		return capabilities{}, ErrCollision
	}
	if options.PrimitiveProviderIdentity != "" && owner != options.PrimitiveProviderIdentity {
		return capabilities{}, ErrCollision
	}
	g.mu.Lock()
	priorOwner := g.observedOwners[name]
	g.mu.Unlock()
	if priorOwner != "" && priorOwner != owner && options.PrimitiveProviderIdentity != owner {
		return capabilities{}, ErrCollision
	}
	ext := c
	allowed := map[string]bool{}
	for _, ns := range options.PrimitiveNamespaces {
		allowed[ns] = true
	}
	result := capabilities{actor: a, connection: Connection{Name: name, ProviderIdentity: owner, Generation: generation}, expires: a.ValidUntil, installed: installed}
	// Capability caching remains bounded even when a source issues long tokens.
	if until := g.now().Add(time.Minute); result.expires.After(until) {
		result.expires = until
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 100; page++ {
		listed := listedFirst
		if page > 0 {
			listed, e = call[primitive.NamespaceListResult](ctx, ext, "namespaces/list", primitive.NamespaceListRequest{Cursor: cursor, Limit: 100})
			if e != nil {
				return capabilities{}, e
			}
		}
		if listed.ProviderIdentity != "" && listed.ProviderIdentity != owner {
			return capabilities{}, ErrCollision
		}
		for _, ns := range listed.Namespaces {
			if seen[ns.Name] {
				return capabilities{}, identity.ErrResource
			}
			seen[ns.Name] = true
			if len(allowed) > 0 && !allowed[ns.Name] {
				continue
			}
			detail, e := call[primitive.NamespaceCapabilities](ctx, ext, "namespaces/get", primitive.NamespaceGetRequest{Namespace: ns.Name})
			if e != nil {
				return capabilities{}, e
			}
			if detail.ProviderIdentity != owner || detail.Namespace != ns.Name {
				return capabilities{}, ErrCollision
			}
			seenKinds := map[string]bool{}
			for _, kind := range detail.Kinds {
				if !identity.ValidResourceKind(kind.Kind) || seenKinds[kind.Kind] {
					return capabilities{}, identity.ErrResource
				}
				seenKinds[kind.Kind] = true
				for _, operation := range kind.Operations {
					method := kind.Methods[operation]
					if method == "" || !installed[method] {
						return capabilities{}, identity.ErrResource
					}
				}
			}
			result.namespaces = append(result.namespaces, detail)
		}
		if listed.Complete {
			if listed.NextCursor != "" {
				return capabilities{}, identity.ErrResource
			}
			break
		}
		if listed.NextCursor == "" || listed.NextCursor == cursor || page == 99 {
			return capabilities{}, identity.ErrResource
		}
		cursor = listed.NextCursor
	}
	if g.generation(name) != generation {
		return capabilities{}, identity.ErrResourceStale
	}
	if e = g.final(ctx, a); e != nil {
		return capabilities{}, e
	}
	g.mu.Lock()
	for k, v := range g.cache {
		if !v.expires.After(g.now()) {
			delete(g.cache, k)
		}
	}
	if len(g.cache) >= 128 {
		g.cache = map[string]capabilities{}
	}
	g.cache[key] = result
	g.observedOwners[name] = owner
	g.mu.Unlock()
	if sequence != nil && !force {
		if err := sequence.record(name, readKey, result); err != nil {
			return capabilities{}, err
		}
	}
	return result, nil
}

type DiscoveredNamespace struct {
	Connection Connection `json:"connection"`
	primitive.NamespaceCapabilities
}

func (g *Gateway) Discover(ctx context.Context) ([]DiscoveredNamespace, error) {
	a, e := g.actor(ctx)
	if e != nil {
		return nil, e
	}
	names, e := g.Manager.Names(ctx)
	if e != nil {
		return nil, e
	}
	sort.Strings(names)
	out := []DiscoveredNamespace{}
	owners := map[string]string{}
	for _, name := range names {
		c, e := g.discover(ctx, a, name)
		if errors.Is(e, ErrUnavailable) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if prior := owners[c.connection.ProviderIdentity]; prior != "" && prior != name {
			return nil, ErrCollision
		}
		owners[c.connection.ProviderIdentity] = name
		for _, ns := range c.namespaces {
			out = append(out, DiscoveredNamespace{Connection: c.connection, NamespaceCapabilities: ns})
		}
	}
	if e = g.final(ctx, a); e != nil {
		return nil, e
	}
	return out, nil
}
func supports(c capabilities, namespace, kind, operation string) bool {
	for _, ns := range c.namespaces {
		if namespace != "" && ns.Namespace != namespace {
			continue
		}
		for _, k := range ns.Kinds {
			if k.Kind == kind {
				for _, op := range k.Operations {
					if op == operation && k.Methods[operation] != "" {
						return true
					}
				}
			}
		}
	}
	return false
}
func operationMethod(c capabilities, namespace, kind, operation string) string {
	for _, ns := range c.namespaces {
		if ns.Namespace == namespace {
			for _, k := range ns.Kinds {
				if k.Kind == kind {
					return k.Methods[operation]
				}
			}
		}
	}
	return ""
}
func plural(kind string) string { return primitive.Plural(kind) }

func (g *Gateway) List(ctx context.Context, kind, namespace string) ([]LocatedResource, error) {
	if !identity.ValidResourceKind(kind) {
		return nil, identity.ErrResource
	}
	a, e := g.actor(ctx)
	if e != nil {
		return nil, e
	}
	discovered, e := g.Discover(ctx)
	if e != nil {
		return nil, e
	}
	out := []LocatedResource{}
	seen := map[string]bool{}
	for _, ns := range discovered {
		if namespace != "" && ns.Namespace != namespace {
			continue
		}
		caps := capabilities{namespaces: []primitive.NamespaceCapabilities{ns.NamespaceCapabilities}}
		if !supports(caps, ns.Namespace, kind, "list") {
			continue
		}
		ext, e := g.client(ctx, ns.Connection.Name)
		if e != nil {
			return nil, e
		}
		cursor := ""
		for page := 0; page < 100; page++ {
			raw, e := toolRaw(ctx, ext, operationMethod(caps, ns.Namespace, kind, "list"), primitive.ListRequest{Namespace: ns.Namespace, Cursor: cursor, Limit: 100})
			if e != nil {
				return nil, e
			}
			listed, e := primitive.DecodeList(raw, kind)
			if e != nil {
				return nil, e
			}
			for _, entry := range listed.Resources {
				uri, e := identity.ParseResourceURI(entry.URI)
				key := ns.Connection.ProviderIdentity + "\x00" + entry.URI
				if e != nil || uri.Kind != kind || uri.Namespace != ns.Namespace || entry.Kind != kind || entry.Namespace != uri.Namespace || entry.Name != uri.Name || seen[key] {
					return nil, identity.ErrResource
				}
				seen[key] = true
				out = append(out, LocatedResource{Connection: ns.Connection, Resource: *entry})
			}
			complete, next := listed.Complete, listed.NextCursor
			if complete {
				if next != "" {
					return nil, identity.ErrResource
				}
				break
			}
			if next == "" || next == cursor || page == 99 {
				return nil, identity.ErrResource
			}
			cursor = next
		}
		if g.generation(ns.Connection.Name) != ns.Connection.Generation {
			return nil, identity.ErrResourceStale
		}
	}
	if e = g.final(ctx, a); e != nil {
		return nil, e
	}
	return out, nil
}

// Get reuses an exact provider locator. Passing a pin prevents default/current
// reselection; both provider drift and definition-byte drift fail closed.
func (g *Gateway) Get(ctx context.Context, connection Connection, ref identity.ResourceRef, pin *identity.ResolvedResource) (*primitive.GetResult, error) {
	return g.get(ctx, connection, ref, pin, false)
}

func (g *Gateway) get(ctx context.Context, connection Connection, ref identity.ResourceRef, pin *identity.ResolvedResource, force bool, menuRead ...bool) (out *primitive.GetResult, err error) {
	// A failed read is part of this operation's provenance. Restoring authority
	// later cannot revive its original selected metadata or release cached rows.
	defer func() { poisonReadSequence(ctx, err) }()
	var scopeErr error
	ctx, scopeErr = ReadContext(ctx)
	if scopeErr != nil {
		return nil, scopeErr
	}
	uri, e := identity.ParseResourceURI(ref.URI)
	if e != nil {
		return nil, identity.ErrResource
	}
	a, e := g.actor(ctx)
	if e != nil {
		return nil, e
	}
	binding, e := g.resolveReadBinding(ctx, a, connection.Name)
	if e != nil {
		return nil, e
	}
	c, e := g.discover(ctx, a, connection.Name, binding)
	if e != nil {
		return nil, e
	}
	if c.connection != connection || !supports(c, uri.Namespace, uri.Kind, "get") {
		return nil, identity.ErrResourceDenied
	}
	if pin != nil {
		if pin.URI != ref.URI || pin.ProviderIdentity != connection.ProviderIdentity || !pin.ValidUntil.After(g.now()) || !pin.ResourceCandidate.Valid() {
			return nil, identity.ErrResourceDenied
		}
		ref.Revision = pin.Selector()
	}
	ext := binding.client
	var result primitive.GetResult
	found := false
	if sequence := sequenceFromContext(ctx); sequence != nil && !force {
		result, found, e = sequence.selectedRead(ctx, connection, pin, binding.key)
	}
	if e == nil {
		e = g.checkReadBinding(ctx, a, binding)
	}
	if e == nil && !found {
		result, e = call[primitive.GetResult](ctx, ext, operationMethod(c, uri.Namespace, uri.Kind, "get"), primitive.GetRequest{URI: ref.URI, Revision: ref.Revision})
	}
	if e != nil {
		return nil, e
	}
	if result.Resource == nil || result.ResolvedResource == nil {
		return nil, identity.ErrResourceDenied
	}
	actual := result.ResolvedResource
	if uri.Kind == "menu" {
		if len(menuRead) != 1 || !menuRead[0] || result.MenuProjection == nil || result.MenuProjection.SchemaVersion != 1 || result.Resource.FormatVersion != 1 || result.Resource.Lifecycle != actual.Kind || !declaresMenuFormat(c, uri.Namespace) || len(result.Resource.Definition) != 0 || len(result.Resource.DefinitionBytes) != 0 || ValidateMenu(1, result.MenuProjection.DefinitionBytes) != nil || identity.ContentFingerprint(result.MenuProjection.DefinitionBytes) != result.MenuProjection.ContentFingerprint {
			return nil, identity.ErrResourceDenied
		}
	} else if result.MenuProjection != nil {
		return nil, identity.ErrResourceDenied
	}
	if len(result.Resource.DefinitionBytes) > 0 {
		if identity.ContentFingerprint(result.Resource.DefinitionBytes) != actual.ContentFingerprint {
			return nil, identity.ErrResourceDenied
		}
		if len(result.Resource.Definition) > 0 && !bytes.Equal(result.Resource.Definition, result.Resource.DefinitionBytes) {
			left, leftErr := semanticJSON(result.Resource.Definition)
			right, rightErr := semanticJSON(result.Resource.DefinitionBytes)
			if leftErr != nil || rightErr != nil || !reflect.DeepEqual(left, right) {
				return nil, identity.ErrResourceDenied
			}
		}
		result.Resource.Definition = append(json.RawMessage(nil), result.Resource.DefinitionBytes...)
	}
	if actual.ProviderIdentity != connection.ProviderIdentity || actual.URI != ref.URI || !actual.ResourceCandidate.Valid() || !actual.ValidUntil.After(g.now()) || actual.AuthorityBinding == "" || result.Resource.URI != ref.URI || result.Resource.ContentFingerprint != actual.ContentFingerprint || (uri.Kind != "menu" && identity.ContentFingerprint(result.Resource.Definition) != actual.ContentFingerprint) {
		return nil, identity.ErrResourceDenied
	}
	if result.Resource.Kind != "" && result.Resource.Kind != uri.Kind || result.Resource.Namespace != "" && result.Resource.Namespace != uri.Namespace || result.Resource.Name != "" && result.Resource.Name != uri.Name || result.Resource.Revision != "" && result.Resource.Revision != actual.Selector() {
		return nil, identity.ErrResourceDenied
	}
	result.Resource.Kind, result.Resource.Namespace, result.Resource.Name = uri.Kind, uri.Namespace, uri.Name
	if ref.Revision != "" && actual.Selector() != ref.Revision {
		return nil, identity.ErrResourceDenied
	}
	if pin != nil {
		if actual.ResourceCandidate != pin.ResourceCandidate || actual.AuthorityBinding != pin.AuthorityBinding {
			return nil, identity.ErrResourceDenied
		}
		if actual.ValidUntil.After(pin.ValidUntil) {
			actual.ValidUntil = pin.ValidUntil
		}
	}
	if actual.ValidUntil.After(a.ValidUntil) {
		actual.ValidUntil = a.ValidUntil
	}
	if sequence := sequenceFromContext(ctx); sequence != nil {
		sequence.constrainLease(actual.ValidUntil)
	}
	if g.generation(connection.Name) != connection.Generation {
		return nil, identity.ErrResourceStale
	}
	if e = g.checkReadBinding(ctx, a, binding); e != nil {
		return nil, e
	}
	if e = g.final(ctx, a); e != nil {
		return nil, e
	}
	if !actual.ValidUntil.After(g.now()) {
		return nil, identity.ErrResourceDenied
	}
	if sequence := sequenceFromContext(ctx); sequence != nil && !force && !found {
		if err := sequence.recordSelectedRead(ctx, connection, pin, &result, binding.key); err != nil {
			return nil, err
		}
	}
	return &result, nil
}
func semanticJSON(raw []byte) (any, error) {
	if !json.Valid(raw) {
		return nil, identity.ErrResource
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if e := decoder.Decode(&value); e != nil {
		return nil, e
	}
	return value, nil
}

// ResolveLocator refuses ambiguous URI owners. Explicit selection is required
// for overlapping namespaces; a connection is never chosen by sort order.
func ResolveLocator(resources []LocatedResource, uri, provider string) (Connection, error) {
	var result Connection
	for _, r := range resources {
		if r.Resource.URI == uri && (provider == "" || r.Connection.ProviderIdentity == provider) {
			if result.Name != "" && result != r.Connection {
				return Connection{}, ErrCollision
			}
			result = r.Connection
		}
	}
	if result.Name == "" {
		return result, fmt.Errorf("%w: resource owner", ErrUnavailable)
	}
	return result, nil
}
