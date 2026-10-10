package resource

import (
	"context"
	"encoding/json"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	producer "github.com/viant/agently-core/service/primitiveproducer"
	mcp "github.com/viant/mcp/server"
	"net/http"
	"time"
)

type LocalResourceBinding = producer.LocalResourceBinding
type LocalResourceValidator = producer.LocalResourceValidator
type LocalResourceAuthorizer = producer.LocalResourceAuthorizer
type LocalRevisionPolicyFactory = producer.LocalRevisionPolicyFactory
type LocalWindowListVisibility = producer.LocalWindowListVisibility
type LocalWindowIndex = producer.LocalWindowIndex
type LocalConfig = producer.LocalConfig
type LocalProvider struct {
	identity                  string
	actor                     ActorResolver
	verify                    ActorVerifier
	authorizer                LocalResourceAuthorizer
	bindings                  map[string]LocalResourceBinding
	validators                map[string]LocalResourceValidator
	windowListVisibility      LocalWindowListVisibility
	menuActionVisibility      MenuActionVisibility
	windowIndex               LocalWindowIndex
	now                       func() time.Time
	definitionValidationCache producer.DefinitionValidationCache
}

func NewLocalProvider(config LocalConfig) (*LocalProvider, error) {
	if _, err := producer.NewLocalProvider(config); err != nil {
		return nil, err
	}
	p := &LocalProvider{identity: config.ProviderIdentity, actor: config.Actor, verify: config.Verify, authorizer: config.Authorize, bindings: map[string]LocalResourceBinding{}, validators: map[string]LocalResourceValidator{}, windowListVisibility: config.WindowListVisibility, windowIndex: config.WindowIndex, menuActionVisibility: config.MenuActionVisibility, now: config.Now, definitionValidationCache: config.DefinitionValidationCache}
	for kind, validator := range config.Validators {
		p.validators[kind] = validator
	}
	for _, b := range config.Bindings {
		p.bindings[b.URI] = b
	}
	return p, nil
}

// neutral delegates all policy, read, filtering and wire behavior to the reusable
// producer; this host facade retains only existing adapter/configuration state.
func (p *LocalProvider) neutral() (*producer.LocalProvider, error) {
	if p == nil {
		return nil, ErrUnavailable
	}
	bindings := make([]LocalResourceBinding, 0, len(p.bindings))
	for _, b := range p.bindings {
		bindings = append(bindings, b)
	}
	config := LocalConfig{ProviderIdentity: p.identity, Actor: p.actor, Verify: p.verify, Authorize: p.authorizer, Bindings: bindings, Validators: p.validators, WindowListVisibility: p.windowListVisibility, WindowIndex: p.windowIndex, Now: p.now, DefinitionValidationCache: p.definitionValidationCache}
	if config.DefinitionValidationCache == nil {
		config.DefinitionValidationCache = func(source identity.ResourceSource, kind string, version int64, fingerprint string, validator LocalResourceValidator) bool {
			cached, ok := source.(interface {
				localDefinitionValidated(string, int64, string) bool
			})
			return ok && pureLocalValidator(kind, validator) && cached.localDefinitionValidated(kind, version, fingerprint)
		}
	}
	if p.menuActionVisibility != nil {
		config.MenuActionVisibility = func(ctx context.Context, a identity.VerifiedActor, u identity.ResourceURI, raw json.RawMessage) (bool, error) {
			return p.menuActionVisibility(ctx, a, u, raw)
		}
	}
	return producer.NewLocalProvider(config)
}
func (p *LocalProvider) ProviderIdentity() string {
	if p == nil {
		return ""
	}
	return p.identity
}
func (p *LocalProvider) NamespaceList(ctx context.Context, in primitive.NamespaceListRequest) (*primitive.NamespaceListResult, error) {
	n, e := p.neutral()
	if e != nil {
		return nil, e
	}
	return n.NamespaceList(ctx, in)
}
func (p *LocalProvider) NamespaceGet(ctx context.Context, in primitive.NamespaceGetRequest) (*primitive.NamespaceCapabilities, error) {
	n, e := p.neutral()
	if e != nil {
		return nil, e
	}
	return n.NamespaceGet(ctx, in)
}
func (p *LocalProvider) List(ctx context.Context, kind string, in primitive.ListRequest) (*primitive.ListResult, error) {
	n, e := p.neutral()
	if e != nil {
		return nil, e
	}
	return n.List(ctx, kind, in)
}
func (p *LocalProvider) Get(ctx context.Context, kind string, in primitive.GetRequest) (*primitive.GetResult, error) {
	n, e := p.neutral()
	if e != nil {
		return nil, e
	}
	return n.Get(ctx, kind, in)
}
func (p *LocalProvider) call(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	n, e := p.neutral()
	if e != nil {
		return nil, e
	}
	return n.Call(ctx, method, raw)
}
func (p *LocalProvider) installedTools() []string {
	n, e := p.neutral()
	if e != nil {
		return nil
	}
	return n.InstalledTools()
}
func NewLocalMCPHandler(p *LocalProvider, protect func(http.Handler) http.Handler) (http.Handler, error) {
	n, e := p.neutral()
	if e != nil {
		return nil, e
	}
	return producer.NewLocalMCPHandler(n, protect)
}
func NewMCPHandler(p *LocalProvider, protect func(http.Handler) http.Handler) (http.Handler, error) {
	return NewLocalMCPHandler(p, protect)
}
func newLocalMCPServer(p *LocalProvider, compact ...bool) (*mcp.Server, error) {
	n, e := p.neutral()
	if e != nil {
		return nil, e
	}
	return producer.NewMCPServer(n, compact...)
}
func sameLocalActor(a, b identity.VerifiedActor) bool {
	return a.Subject == b.Subject && a.Issuer == b.Issuer && a.TenantID == b.TenantID && a.AccountID == b.AccountID && a.IdentityRevision == b.IdentityRevision
}
