package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	workspacewindow "github.com/viant/agently-core/service/ui/window"
	"github.com/viant/forge/backend/reporting/registry"
	forgeTypes "github.com/viant/forge/backend/types"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
	protocol "github.com/viant/mcp-protocol/server"
	mcp "github.com/viant/mcp/server"
)

// LocalResourceBinding gives a trusted host an explicit logical identity and
// source. The loader must read the configured local YAML/JSON asset, never a
// path or URI selected by a tool caller.
type LocalResourceBinding struct {
	URI           string
	Title         string
	Description   string
	FormatVersion int64
	Resolver      func(context.Context, identity.VerifiedActor, string) (*identity.ResourceResolver, error)
}

type LocalResourceValidator func(int64, json.RawMessage) error
type LocalResourceAuthorizer func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error
type LocalRevisionPolicyFactory func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) (identity.ResourceRevisionPolicy, error)
type LocalWindowListVisibility func(context.Context, identity.VerifiedActor, LocalResourceBinding) (bool, error)
type LocalWindowIndex func(context.Context) ([]primitive.ResourceState, error)

// LocalConfig installs one authoritative local catalog. It has no aggregation
// or mutation path; only list/get operations are advertised.
type LocalConfig struct {
	ProviderIdentity     string
	Actor                ActorResolver
	Verify               ActorVerifier
	Authorize            LocalResourceAuthorizer
	Bindings             []LocalResourceBinding
	Validators           map[string]LocalResourceValidator
	WindowListVisibility LocalWindowListVisibility
	WindowIndex          LocalWindowIndex
	Now                  func() time.Time
}

type LocalProvider struct {
	identity             string
	actor                ActorResolver
	verify               ActorVerifier
	authorizer           LocalResourceAuthorizer
	bindings             map[string]LocalResourceBinding
	validators           map[string]LocalResourceValidator
	windowListVisibility LocalWindowListVisibility
	windowIndex          LocalWindowIndex
	now                  func() time.Time
}

// ProviderIdentity is immutable provenance configured by the trusted host.
func (p *LocalProvider) ProviderIdentity() string {
	if p == nil {
		return ""
	}
	return p.identity
}

func NewLocalProvider(config LocalConfig) (*LocalProvider, error) {
	if strings.TrimSpace(config.ProviderIdentity) == "" || strings.TrimSpace(config.ProviderIdentity) != config.ProviderIdentity || config.Actor == nil || config.Verify == nil || config.Authorize == nil || len(config.Bindings) == 0 {
		return nil, fmt.Errorf("local resource provider requires identity, verified actor, namespace authorizer, and bindings")
	}
	if config.WindowListVisibility != nil && config.WindowIndex == nil {
		return nil, fmt.Errorf("window list visibility requires a trusted static window index")
	}
	p := &LocalProvider{identity: config.ProviderIdentity, actor: config.Actor, verify: config.Verify, authorizer: config.Authorize, bindings: map[string]LocalResourceBinding{}, validators: map[string]LocalResourceValidator{}, windowListVisibility: config.WindowListVisibility, windowIndex: config.WindowIndex, now: config.Now}
	if p.now == nil {
		p.now = time.Now
	}
	for kind, validator := range config.Validators {
		if !identity.ValidResourceKind(kind) || validator == nil {
			return nil, fmt.Errorf("invalid local resource validator")
		}
		p.validators[kind] = validator
	}
	for _, binding := range config.Bindings {
		uri, err := identity.ParseResourceURI(binding.URI)
		if err != nil || primitive.Plural(uri.Kind) == "" || binding.Resolver == nil || binding.FormatVersion < 1 || p.validators[uri.Kind] == nil {
			return nil, fmt.Errorf("local resource binding requires an explicit resolver, format version, and kind validator")
		}
		if _, exists := p.bindings[binding.URI]; exists {
			return nil, fmt.Errorf("duplicate local resource binding")
		}
		p.bindings[binding.URI] = binding
	}
	return p, nil
}

// WorkspaceWindowBindings adapts the canonical local workspace loader into
// explicit working-only provider bindings. Host policy remains injected per
// verified actor and operation; this helper grants no access by itself.
func WorkspaceWindowBindings(root, providerIdentity string, bindings []workspacewindow.ResourceBinding, policy LocalRevisionPolicyFactory, enrich workspacewindow.WorkspaceWindowEnricher) ([]LocalResourceBinding, error) {
	if strings.TrimSpace(providerIdentity) == "" || policy == nil || len(bindings) == 0 {
		return nil, fmt.Errorf("workspace window bindings require provider identity and revision policy")
	}
	source, err := workspacewindow.NewWorkspaceResourceSource(root, bindings, enrich)
	if err != nil {
		return nil, err
	}
	out := make([]LocalResourceBinding, 0, len(bindings))
	for _, configured := range bindings {
		uri, err := identity.ParseResourceURI(configured.URI)
		if err != nil || uri.Kind != "window" {
			return nil, fmt.Errorf("workspace resource binding must name a window")
		}
		binding := configured
		out = append(out, LocalResourceBinding{URI: binding.URI, Title: binding.WindowKey, FormatVersion: 2, Resolver: func(ctx context.Context, actor identity.VerifiedActor, action string) (*identity.ResourceResolver, error) {
			currentURI, err := identity.ParseResourceURI(binding.URI)
			if err != nil {
				return nil, identity.ErrResourceDenied
			}
			revisionPolicy, err := policy(ctx, actor, currentURI, action)
			if err != nil {
				return nil, err
			}
			if revisionPolicy == nil {
				return nil, identity.ErrResourceDenied
			}
			return &identity.ResourceResolver{ProviderIdentity: providerIdentity, Source: source, Policy: revisionPolicy}, nil
		}})
	}
	return out, nil
}

func (p *LocalProvider) nowTime() time.Time {
	if p != nil && p.now != nil {
		return p.now()
	}
	return time.Now()
}

func (p *LocalProvider) currentActor(ctx context.Context) (identity.VerifiedActor, error) {
	if p == nil || p.actor == nil || p.verify == nil || ctx == nil || ctx.Err() != nil {
		return identity.VerifiedActor{}, identity.ErrResourceDenied
	}
	actor, err := p.actor(ctx)
	if err != nil {
		return identity.VerifiedActor{}, err
	}
	if !actor.Valid(p.nowTime()) || ctx.Err() != nil {
		return identity.VerifiedActor{}, identity.ErrResourceDenied
	}
	if err := p.verify(ctx, actor); err != nil {
		return identity.VerifiedActor{}, err
	}
	return actor, nil
}

func (p *LocalProvider) finishActor(ctx context.Context, actor identity.VerifiedActor) error {
	if p == nil || p.actor == nil || p.verify == nil || ctx == nil || ctx.Err() != nil || !actor.Valid(p.nowTime()) {
		return identity.ErrResourceDenied
	}
	fresh, err := p.actor(ctx)
	if err != nil {
		return err
	}
	if !fresh.Valid(p.nowTime()) || !sameLocalActor(actor, fresh) {
		return identity.ErrResourceDenied
	}
	return p.verify(ctx, fresh)
}

func sameLocalActor(left, right identity.VerifiedActor) bool {
	return left.Subject == right.Subject && left.Issuer == right.Issuer && left.TenantID == right.TenantID && left.AccountID == right.AccountID && left.IdentityRevision == right.IdentityRevision
}

func (p *LocalProvider) authorize(ctx context.Context, actor identity.VerifiedActor, uri identity.ResourceURI, action string) error {
	if p == nil || p.authorizer == nil || !uri.Valid() {
		return identity.ErrResourceDenied
	}
	if err := p.authorizer(ctx, actor, uri, action); err != nil {
		return err
	}
	return nil
}

func (p *LocalProvider) checkedRead(ctx context.Context, actor identity.VerifiedActor, binding LocalResourceBinding, uri identity.ResourceURI, action string, requested string) (json.RawMessage, *identity.ResolvedResource, error) {
	if err := p.authorize(ctx, actor, uri, action); err != nil {
		return nil, nil, err
	}
	resolver, err := binding.Resolver(ctx, actor, action)
	if err != nil {
		return nil, nil, err
	}
	if resolver == nil || resolver.ProviderIdentity != p.identity {
		return nil, nil, identity.ErrResourceDenied
	}
	if requested != "" && requested != identity.WorkingCandidate {
		return nil, nil, identity.ErrResourceDenied
	}
	// Preserve omission so host policy can honor an authorized main/exposure
	// selection. Explicit working is supported; stamped selectors are not.
	pin, err := resolver.Resolve(ctx, identity.ResourceRef{URI: uri.String(), Revision: requested})
	if err != nil {
		return nil, nil, err
	}
	if pin == nil || pin.ProviderIdentity != p.identity || pin.URI != uri.String() || pin.Kind != identity.WorkingCandidate || pin.Revision != "" || !pin.ValidUntil.After(p.nowTime()) {
		return nil, nil, identity.ErrResourceDenied
	}
	raw, fresh, err := resolver.ReadResolved(ctx, *pin)
	if err != nil {
		return nil, nil, err
	}
	if fresh == nil || fresh.ProviderIdentity != p.identity || fresh.URI != pin.URI || fresh.ResourceCandidate != pin.ResourceCandidate || fresh.AuthorityBinding != pin.AuthorityBinding {
		return nil, nil, identity.ErrResourceDenied
	}
	validator := p.validators[uri.Kind]
	validated := false
	if cached, ok := resolver.Source.(interface {
		localDefinitionValidated(string, int64, string) bool
	}); ok && pureLocalValidator(uri.Kind, validator) {
		validated = cached.localDefinitionValidated(uri.Kind, binding.FormatVersion, fresh.ContentFingerprint)
	}
	if validator == nil || !validated && validator(binding.FormatVersion, raw) != nil {
		return nil, nil, fmt.Errorf("invalid local %s definition", uri.Kind)
	}
	if err := p.authorize(ctx, actor, uri, action); err != nil {
		return nil, nil, err
	}
	if err := p.finishActor(ctx, actor); err != nil {
		return nil, nil, err
	}
	if cached, ok := resolver.Source.(interface {
		localDefinitionCurrent(context.Context, identity.ResourceURI, identity.ResourceCandidate) error
	}); ok {
		if err := cached.localDefinitionCurrent(ctx, uri, fresh.ResourceCandidate); err != nil {
			return nil, nil, err
		}
	}
	if ctx.Err() != nil || !fresh.ValidUntil.After(p.nowTime()) {
		return nil, nil, identity.ErrResourceDenied
	}
	return append(json.RawMessage(nil), raw...), fresh, nil
}

func (p *LocalProvider) NamespaceList(ctx context.Context, input primitive.NamespaceListRequest) (*primitive.NamespaceListResult, error) {
	offset, limit, err := pageRequest(input.Cursor, input.Limit)
	if err != nil {
		return nil, err
	}
	actor, err := p.currentActor(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]map[string]bool{}
	for _, binding := range p.bindings {
		u, _ := identity.ParseResourceURI(binding.URI)
		if names[u.Namespace] == nil {
			names[u.Namespace] = map[string]bool{}
		}
		names[u.Namespace][u.Kind] = true
	}
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)
	visible := make([]primitive.Namespace, 0, len(ordered))
	for _, name := range ordered {
		probe := identity.ResourceURI{Kind: "resource", Namespace: name, Name: "catalog"}
		if err := p.authorize(ctx, actor, probe, "namespace.list"); err != nil {
			if errors.Is(err, identity.ErrResourceDenied) {
				continue
			}
			return nil, err
		}
		kinds := make([]string, 0, len(names[name]))
		for kind := range names[name] {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		visible = append(visible, primitive.Namespace{Name: name, Kinds: kinds})
	}
	if err := p.finishActor(ctx, actor); err != nil {
		return nil, err
	}
	start, end, next, complete, err := pageBounds(offset, limit, len(visible))
	if err != nil {
		return nil, err
	}
	out := &primitive.NamespaceListResult{ProviderIdentity: p.identity, Namespaces: visible[start:end], NextCursor: next, Complete: complete}
	return out, nil
}

func (p *LocalProvider) NamespaceGet(ctx context.Context, input primitive.NamespaceGetRequest) (*primitive.NamespaceCapabilities, error) {
	if strings.TrimSpace(input.Namespace) == "" || strings.TrimSpace(input.Namespace) != input.Namespace {
		return nil, identity.ErrResource
	}
	actor, err := p.currentActor(ctx)
	if err != nil {
		return nil, err
	}
	probe := identity.ResourceURI{Kind: "resource", Namespace: input.Namespace, Name: "catalog"}
	if err := p.authorize(ctx, actor, probe, "namespace.get"); err != nil {
		return nil, err
	}
	typesByKind := map[string]map[int64]bool{}
	for _, binding := range p.bindings {
		u, _ := identity.ParseResourceURI(binding.URI)
		if u.Namespace == input.Namespace {
			if typesByKind[u.Kind] == nil {
				typesByKind[u.Kind] = map[int64]bool{}
			}
			typesByKind[u.Kind][binding.FormatVersion] = true
		}
	}
	if len(typesByKind) == 0 {
		return nil, identity.ErrResourceDenied
	}
	kinds := make([]string, 0, len(typesByKind))
	for k := range typesByKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	out := &primitive.NamespaceCapabilities{ProviderIdentity: p.identity, Namespace: input.Namespace, Kinds: []primitive.KindSupport{}}
	for _, kind := range kinds {
		versions := make([]int64, 0, len(typesByKind[kind]))
		for v := range typesByKind[kind] {
			versions = append(versions, v)
		}
		sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
		ops := []string{"list", "get"}
		out.Kinds = append(out.Kinds, primitive.KindSupport{Kind: kind, FormatVersions: versions, Operations: ops, TestSupported: false, Methods: primitive.Methods(kind, ops)})
	}
	if err := p.authorize(ctx, actor, probe, "namespace.get"); err != nil {
		return nil, err
	}
	if err := p.finishActor(ctx, actor); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *LocalProvider) List(ctx context.Context, kind string, input primitive.ListRequest) (*primitive.ListResult, error) {
	plural := primitive.Plural(kind)
	offset, limit, pageErr := pageRequest(input.Cursor, input.Limit)
	if plural == "" || pageErr != nil {
		return nil, identity.ErrResource
	}
	if kind == "window" && p.windowIndex != nil {
		return p.listIndexedWindows(ctx, input, offset, limit)
	}
	actor, err := p.currentActor(ctx)
	if err != nil {
		return nil, err
	}
	uris := make([]string, 0, len(p.bindings))
	for value := range p.bindings {
		u, _ := identity.ParseResourceURI(value)
		if u.Kind == kind && (input.Namespace == "" || u.Namespace == input.Namespace) {
			uris = append(uris, value)
		}
	}
	sort.Strings(uris)
	out := &primitive.ListResult{Resources: []*primitive.ResourceState{}, Complete: true}
	for _, value := range uris {
		binding := p.bindings[value]
		uri, _ := identity.ParseResourceURI(value)
		_, pin, e := p.checkedRead(ctx, actor, binding, uri, "resource.list", identity.WorkingCandidate)
		if e != nil {
			if errors.Is(e, identity.ErrResourceDenied) {
				continue
			}
			return nil, e
		}
		out.Resources = append(out.Resources, &primitive.ResourceState{Kind: kind, Namespace: uri.Namespace, Name: uri.Name, URI: value, Title: binding.Title, Lifecycle: "working", Revision: identity.WorkingCandidate, FormatVersion: binding.FormatVersion, ContentFingerprint: pin.ContentFingerprint})
	}
	if err := p.finishActor(ctx, actor); err != nil {
		return nil, err
	}
	start, end, next, complete, err := pageBounds(offset, limit, len(out.Resources))
	if err != nil {
		return nil, err
	}
	out.Resources, out.NextCursor, out.Complete = out.Resources[start:end], next, complete
	return out, nil
}

// listIndexedWindows exposes only static snapshot metadata. It deliberately
// avoids resolver selection and definition reads; Get remains the authorized
// exact-pin path for full definitions.
func (p *LocalProvider) listIndexedWindows(ctx context.Context, input primitive.ListRequest, offset, limit int) (*primitive.ListResult, error) {
	actor, err := p.currentActor(ctx)
	if err != nil {
		return nil, err
	}
	configuredNamespaces := map[string]bool{}
	for value := range p.bindings {
		uri, parseErr := identity.ParseResourceURI(value)
		if parseErr == nil && uri.Kind == "window" {
			configuredNamespaces[uri.Namespace] = true
		}
	}
	if input.Namespace != "" && !configuredNamespaces[input.Namespace] {
		return nil, identity.ErrResourceDenied
	}
	before, err := p.windowIndex(ctx)
	if err != nil {
		return nil, err
	}
	beforeByURI, err := localWindowIndexMap(before)
	if err != nil {
		return nil, err
	}
	uris := make([]string, 0, len(beforeByURI))
	for uri := range beforeByURI {
		uris = append(uris, uri)
	}
	sort.Strings(uris)
	visible := make([]*primitive.ResourceState, 0, len(uris))
	for _, value := range uris {
		state := beforeByURI[value]
		uri, _ := identity.ParseResourceURI(value)
		binding, configured := p.bindings[value]
		if !configured || input.Namespace != "" && uri.Namespace != input.Namespace {
			continue
		}
		if state.FormatVersion != binding.FormatVersion || binding.Title != "" && state.Title != binding.Title {
			return nil, identity.ErrResourceStale
		}
		if err := p.authorize(ctx, actor, uri, "resource.list"); err != nil {
			if errors.Is(err, identity.ErrResourceDenied) {
				continue
			}
			return nil, err
		}
		if p.windowListVisibility != nil {
			allowed, err := p.windowListVisibility(ctx, actor, binding)
			if err != nil {
				if errors.Is(err, identity.ErrResourceDenied) {
					continue
				}
				return nil, err
			}
			if !allowed {
				continue
			}
		}
		copy := state
		visible = append(visible, &copy)
	}
	filtered := make([]*primitive.ResourceState, 0, len(visible))
	for _, state := range visible {
		uri, _ := identity.ParseResourceURI(state.URI)
		if err := p.authorize(ctx, actor, uri, "resource.list"); err != nil {
			if errors.Is(err, identity.ErrResourceDenied) {
				continue
			}
			return nil, err
		}
		if p.windowListVisibility != nil {
			binding, ok := p.bindings[state.URI]
			if !ok {
				return nil, identity.ErrResourceStale
			}
			allowed, err := p.windowListVisibility(ctx, actor, binding)
			if err != nil {
				if errors.Is(err, identity.ErrResourceDenied) {
					continue
				}
				return nil, err
			}
			if !allowed {
				continue
			}
		}
		filtered = append(filtered, state)
	}
	after, err := p.windowIndex(ctx)
	if err != nil {
		return nil, err
	}
	afterByURI, err := localWindowIndexMap(after)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(beforeByURI, afterByURI) {
		return nil, identity.ErrResourceStale
	}
	if err := p.finishActor(ctx, actor); err != nil {
		return nil, err
	}
	start, end, next, complete, err := pageBounds(offset, limit, len(filtered))
	if err != nil {
		return nil, err
	}
	return &primitive.ListResult{Resources: filtered[start:end], NextCursor: next, Complete: complete}, nil
}

func localWindowIndexMap(values []primitive.ResourceState) (map[string]primitive.ResourceState, error) {
	if len(values) > 10000 {
		return nil, identity.ErrResourceDenied
	}
	result := make(map[string]primitive.ResourceState, len(values))
	for _, value := range values {
		uri, err := identity.ParseResourceURI(value.URI)
		candidate := identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: value.ContentFingerprint}
		if err != nil || uri.Kind != "window" || value.Kind != "window" || value.Namespace != uri.Namespace || value.Name != uri.Name || value.Lifecycle != identity.WorkingCandidate || value.Revision != identity.WorkingCandidate || value.Stamp != 0 || value.DraftRevision != 0 || value.LatestStamp != 0 || value.FormatVersion < 1 || !candidate.Valid() || len(value.Definition) != 0 || len(value.DefinitionBytes) != 0 || len(value.Dependencies) != 0 || value.Replay {
			return nil, identity.ErrResource
		}
		if _, duplicate := result[value.URI]; duplicate {
			return nil, identity.ErrResource
		}
		result[value.URI] = value
	}
	return result, nil
}

func pageRequest(cursor string, limit int) (int, int, error) {
	if limit < 0 || limit > 100 {
		return 0, 0, identity.ErrResource
	}
	offset := 0
	if cursor != "" {
		value, err := strconv.Atoi(cursor)
		if err != nil || value < 0 || strconv.Itoa(value) != cursor {
			return 0, 0, identity.ErrResource
		}
		offset = value
	}
	if limit == 0 {
		limit = 100
	}
	return offset, limit, nil
}
func pageBounds(offset, limit, total int) (int, int, string, bool, error) {
	if offset > total {
		return 0, 0, "", false, identity.ErrResource
	}
	end := offset + limit
	if end > total {
		end = total
	}
	complete := end == total
	next := ""
	if !complete {
		next = strconv.Itoa(end)
	}
	return offset, end, next, complete, nil
}

func (p *LocalProvider) Get(ctx context.Context, kind string, input primitive.GetRequest) (*primitive.GetResult, error) {
	uri, err := identity.ParseResourceURI(input.URI)
	if err != nil || uri.Kind != kind || input.WorkspaceID != "" {
		return nil, identity.ErrResource
	}
	binding, ok := p.bindings[input.URI]
	if !ok {
		return nil, identity.ErrResourceDenied
	}
	actor, err := p.currentActor(ctx)
	if err != nil {
		return nil, err
	}
	raw, pin, err := p.checkedRead(ctx, actor, binding, uri, "resource.get", input.Revision)
	if err != nil {
		return nil, err
	}
	return &primitive.GetResult{Resource: &primitive.ResourceState{Kind: kind, Namespace: uri.Namespace, Name: uri.Name, URI: input.URI, Title: binding.Title, Lifecycle: "working", Revision: identity.WorkingCandidate, FormatVersion: binding.FormatVersion, ContentFingerprint: pin.ContentFingerprint, Definition: raw, DefinitionBytes: append([]byte(nil), raw...)}, ResolvedResource: pin}, nil
}

// ValidateWindowBundle validates the versioned local Forge window bundle.
func ValidateWindowBundle(formatVersion int64, raw json.RawMessage) error {
	if formatVersion != 2 {
		return fmt.Errorf("unsupported window format")
	}
	var bundle forgeTypes.WindowResourceEnvelope
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&bundle); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing window data")
	}
	return bundle.Validate()
}

// ValidateReportEnvelope validates the supported local report definition form.
func ValidateReportEnvelope(formatVersion int64, raw json.RawMessage) error {
	if formatVersion != 1 {
		return fmt.Errorf("unsupported report format")
	}
	var envelope registry.ReportEnvelope
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&envelope); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF || envelope.SchemaVersion != 1 || !validJSONObject(envelope.ReportDocument) {
		return fmt.Errorf("invalid report envelope")
	}
	switch envelope.Format {
	case "":
		if !validJSONObject(envelope.ReportSpec) || len(envelope.BuilderDefinition) != 0 {
			return fmt.Errorf("invalid native report envelope")
		}
	case registry.AuthoredReportFormat:
		if envelope.BuilderRef == "" || !validJSONObject(envelope.BuilderDefinition) {
			return fmt.Errorf("invalid authored report envelope")
		}
	default:
		return fmt.Errorf("unsupported report envelope")
	}
	return nil
}

func validJSONObject(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &value) == nil && value != nil
}

func (p *LocalProvider) installedTools() []string {
	tools := []string{"namespaces/list", "namespaces/get"}
	kinds := map[string]bool{}
	for _, b := range p.bindings {
		u, _ := identity.ParseResourceURI(b.URI)
		kinds[u.Kind] = true
	}
	ordered := make([]string, 0, len(kinds))
	for k := range kinds {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	for _, kind := range ordered {
		plural := primitive.Plural(kind)
		tools = append(tools, plural+"/list", plural+"/get")
	}
	return tools
}

func (p *LocalProvider) call(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "namespaces/list":
		var in primitive.NamespaceListRequest
		if err := strictDecode(raw, &in); err != nil {
			return nil, err
		}
		return p.NamespaceList(ctx, in)
	case "namespaces/get":
		var in primitive.NamespaceGetRequest
		if err := strictDecode(raw, &in); err != nil {
			return nil, err
		}
		return p.NamespaceGet(ctx, in)
	}
	parts := strings.Split(method, "/")
	if len(parts) != 2 || parts[1] != "list" && parts[1] != "get" {
		return nil, identity.ErrResource
	}
	kind := primitive.Kind(parts[0])
	if kind == "" {
		return nil, identity.ErrResource
	}
	if parts[1] == "list" {
		var in primitive.ListRequest
		if err := strictDecode(raw, &in); err != nil {
			return nil, err
		}
		listed, err := p.List(ctx, kind, in)
		if err != nil {
			return nil, err
		}
		out := map[string]any{parts[0]: listed.Resources, "complete": listed.Complete}
		if listed.NextCursor != "" {
			out["nextCursor"] = listed.NextCursor
		}
		return out, nil
	}
	var in primitive.GetRequest
	if err := strictDecode(raw, &in); err != nil {
		return nil, err
	}
	return p.Get(ctx, kind, in)
}

func strictDecode(raw json.RawMessage, value any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return identity.ErrResource
	}
	if d.Decode(new(any)) != io.EOF {
		return identity.ErrResource
	}
	return nil
}

func inputSchema(v any) (schema.ToolInputSchema, error) {
	var result schema.ToolInputSchema
	err := result.Load(v)
	return result, err
}

type localMCPHandler struct {
	*protocol.DefaultHandler
	provider *LocalProvider
}

func (h *localMCPHandler) Implements(method string) bool {
	for _, name := range h.provider.installedTools() {
		if name == method {
			return true
		}
	}
	return h.DefaultHandler.Implements(method)
}
func (h *localMCPHandler) Initialize(ctx context.Context, request *schema.InitializeRequestParams, result *schema.InitializeResult) {
	h.DefaultHandler.Initialize(ctx, request, result)
	h.addMetadata(&result.Capabilities)
}
func (h *localMCPHandler) Discover(ctx context.Context, result *schema.DiscoverResult) {
	h.DefaultHandler.Discover(ctx, result)
	h.addMetadata(&result.Capabilities)
}
func (h *localMCPHandler) addMetadata(c *schema.ServerCapabilities) {
	if c.Extensions == nil {
		c.Extensions = map[string]map[string]interface{}{}
	}
	c.Extensions[primitive.AuthoringExtension] = map[string]interface{}{"version": 1, "providerIdentity": h.provider.identity, "transport": "tools/call"}
}

// NewLocalMCPHandler builds the standard MCP tools/list and tools/call path.
// The protection wrapper must derive the actor from authenticated transport
// state and is mandatory; request bodies never supply identity.
func NewLocalMCPHandler(provider *LocalProvider, protect func(http.Handler) http.Handler) (http.Handler, error) {
	if provider == nil || protect == nil {
		return nil, ErrUnavailable
	}
	protocolServer, err := newLocalMCPServer(provider)
	if err != nil {
		return nil, err
	}
	protocolServer.UseStreamableHTTP(true)
	var handler http.Handler = protocolServer.HTTP(context.Background(), "").Handler
	handler = protect(handler)
	if handler == nil {
		return nil, ErrUnavailable
	}
	return handler, nil
}

func newLocalMCPServer(provider *LocalProvider, compact ...bool) (*mcp.Server, error) {
	compactNative := len(compact) > 0 && compact[0]
	return mcp.New(mcp.WithImplementation(schema.Implementation{Name: "agently-local-resources", Version: "1"}), mcp.WithStreamableURI("/mcp"), mcp.WithRootRedirect(false), mcp.WithNewHandler(func(_ context.Context, n transport.Notifier, l logger.Logger, c client.Operations) (protocol.Handler, error) {
		h := &localMCPHandler{DefaultHandler: protocol.NewDefaultHandler(n, l, c), provider: provider}
		for _, method := range provider.installedTools() {
			name := method
			description := "List authorized local resource definitions"
			var sample any = primitive.ListRequest{}
			if strings.HasPrefix(name, "namespaces/") {
				description = "Read local resource namespace capabilities"
				sample = primitive.NamespaceGetRequest{}
				if name == "namespaces/list" {
					sample = primitive.NamespaceListRequest{}
				}
			} else if strings.HasSuffix(name, "/get") {
				description = "Read an authorized local resource definition"
				sample = primitive.GetRequest{}
			}
			input, schemaErr := inputSchema(sample)
			if schemaErr != nil {
				return nil, schemaErr
			}
			h.Registry.RegisterToolWithSchema(name, description, input, nil, func(ctx context.Context, r *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
				raw, e := json.Marshal(r.Params.Arguments)
				if e != nil {
					return nil, jsonrpc.NewInvalidParamsError("invalid resource request", nil)
				}
				value, e := provider.call(ctx, name, raw)
				failed := e != nil
				if e != nil {
					value = map[string]string{"code": "resource_unavailable", "message": "local resource operation denied or unavailable"}
				}
				compactGet := false
				if compactNative && !failed {
					if get, ok := value.(*primitive.GetResult); ok && get.Resource != nil && len(get.Resource.DefinitionBytes) > 0 {
						resource := *get.Resource
						resource.Definition = nil
						projected := *get
						projected.Resource = &resource
						value = &projected
						compactGet = true
					}
				}
				encoded := []byte("Resource definition returned in structuredContent.")
				var marshalErr error
				if !compactGet {
					encoded, marshalErr = json.Marshal(value)
				}
				if marshalErr != nil {
					return nil, jsonrpc.NewInternalError("resource response unavailable", nil)
				}
				return &schema.CallToolResult{IsError: &failed, StructuredContent: value, Content: []schema.CallToolResultContentElem{schema.TextContent{Type: "text", Text: string(encoded)}}}, nil
			})
			entry, _ := h.ToolRegistry.Get(name)
			entry.Metadata.Meta = map[string]interface{}{primitive.AuthoringExtension: map[string]interface{}{"version": 1, "providerIdentity": provider.identity, "transport": "tools/call"}}
		}
		return h, nil
	}))
}

// NewMCPHandler is the conventional constructor for a protected local resource
// endpoint. The protection middleware is required and must install identity
// through request context before tool dispatch.
func NewMCPHandler(provider *LocalProvider, protect func(http.Handler) http.Handler) (http.Handler, error) {
	return NewLocalMCPHandler(provider, protect)
}

var _ interface{ Implements(string) bool } = (*localMCPHandler)(nil)
