package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/viant/afs"
	httpui "github.com/viant/agently-core/adapter/http/ui"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/service/policy"
	"github.com/viant/authz"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/service/meta"
	"github.com/viant/forge/backend/types"
)

type targetPipelineSource struct {
	raw     json.RawMessage
	allowed bool
}

func (s *targetPipelineSource) Candidates(context.Context, identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	return []identity.ResourceCandidate{{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(s.raw)}}, nil
}
func (s *targetPipelineSource) ReadCandidate(context.Context, identity.ResourceURI, identity.ResourceCandidate) (json.RawMessage, error) {
	return s.raw, nil
}

type targetPipelinePolicy struct{ source *targetPipelineSource }

func (p targetPipelinePolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if !p.source.allowed {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "verified-user-account", ValidUntil: time.Now().Add(time.Minute)}, nil
}

type targetPipelineExecutor struct {
	calls  int
	during func()
	seen   map[string]interface{}
}

func (e *targetPipelineExecutor) Execute(_ context.Context, _ string, args map[string]interface{}) (string, error) {
	e.calls++
	e.seen = args
	if e.during != nil {
		e.during()
	}
	return `{"data":[{"selected":"phone"}]}`, nil
}
func targetPipelineEnvelope(t *testing.T) json.RawMessage {
	t.Helper()
	envelope := types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Variants: map[string]types.WindowResourceVariant{}}
	for _, p := range []struct {
		target types.WindowTarget
		name   string
	}{{types.WindowTarget{}, "desktop"}, {types.WindowTarget{Platform: "ios", FormFactor: "phone"}, "phone"}} {
		ds := dsproto.DataSource{ID: "rows", DataSource: types.DataSource{Selectors: &types.Selectors{Data: "data"}, Parameters: []types.Parameter{{Name: "range", Default: p.name}}}, Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "fixture", Method: "read", Pinned: map[string]interface{}{"targetDefault": p.name}}}
		descriptor, _ := json.Marshal(ds)
		descriptor, _ = types.CanonicalWindowDescriptor(descriptor)
		variant := types.WindowResourceVariant{Window: &types.Window{View: types.View{Content: &types.Container{ID: p.name + "Root"}}, DataSource: map[string]types.DataSource{"rows": ds.DataSource}, ResourceDependencies: map[string]string{"rows": identity.ContentFingerprint(descriptor)}}, DataSources: map[string]json.RawMessage{"rows": descriptor}}
		key, _ := types.WindowVariantFingerprint(variant)
		envelope.Variants[key] = variant
		envelope.Targets = append(envelope.Targets, types.WindowTargetBinding{Target: p.target, Variant: key})
	}
	raw, _ := json.Marshal(envelope)
	return raw
}
func TestHTTPWindowTargetExecutesApprovedDescriptorWithoutCurrentStoreFallback(t *testing.T) {
	source := &targetPipelineSource{raw: targetPipelineEnvelope(t), allowed: true}
	resolver := &identity.ResourceResolver{Source: source, Policy: targetPipelinePolicy{source}}
	resource := authz.Resource{Kind: "window", ID: "window://example/sales", Version: "working", Tenant: "tenant"}
	facts := authz.Facts{Subject: "user", Issuer: "issuer", Tenant: "tenant", Roles: []string{"reader"}, ValidUntil: time.Now().Add(time.Hour)}
	prepared, err := PrepareStaticAuthorization(StaticAuthorizationRegistration{ProviderRef: "fixture", CapabilityMappingRef: "fixture", PolicyVersion: "1", Service: &authz.Service{Provider: staticAuthFacts{facts}, Store: staticAuthStore{authz.Document{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{"execute": {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}}, Account: func(context.Context, authz.Facts) (string, error) { return "account", nil }, AuthorityRevision: func(context.Context, authz.Facts, string) (string, time.Time, error) {
		return "identity", facts.ValidUntil, nil
	}, GateEvaluator: staticGateBridge(func(context.Context, authz.Resource, string, []authz.Entity) (bool, string, time.Time, string, string, string, string, error) {
		return true, "gate", facts.ValidUntil, "user", "issuer", "tenant", "account", nil
	}), BackendMapper: func(_ context.Context, operation, id string, _ map[string]interface{}) (authz.Resource, string, []authz.Entity, string, error) {
		if operation != "datasource.fetch" || id != resource.ID+"#rows" {
			return authz.Resource{}, "", nil, "", policy.ErrDenied
		}
		return resource, "execute", nil, "", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(context.Context, string) (*identity.ResourceResolver, identity.ResourceRef, error) {
		return resolver, identity.ResourceRef{URI: resource.ID}, nil
	}
	if err = prepared.WithWindowResourceResolver(resolve); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	catalog, err := forge.NewMetadataWindowCatalog(meta.New(afs.New(), root), root, []forge.SavedWindow{{WindowDefinitionSummary: forge.WindowDefinitionSummary{WindowID: "sales", ResourceURI: resource.ID}, Key: "never-use-source-file"}}, forge.WithWindowResourceResolver(resolve))
	if err != nil {
		t.Fatal(err)
	}
	if err = prepared.WithWindowTargetVerifier(catalog.VerifyWindowTarget); err != nil {
		t.Fatal(err)
	}
	bridge := forge.NewService(&forge.Config{WindowDefinitions: catalog})
	handler := httpui.NewEmbeddedHandlerWithAuthorization(root, nil, nil, nil, httpui.WithWindowResourceProvider(bridge))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/window/sales?platform=ios&formFactor=phone&surface=app", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("target HTTP status %d", recorder.Code)
	}
	var result struct {
		Data types.Window `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.View.Content.ID != "phoneRoot" || result.Data.ResourceTarget == nil || result.Data.ResourceTarget.SelectionToken == "" {
		t.Fatal("HTTP did not carry approved phone selection")
	}
	executor := &targetPipelineExecutor{}
	store := datasource.NewMemoryStore()
	store.Put(&dsproto.DataSource{ID: "rows", Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "forbidden", Method: "current"}})
	svc := datasource.New(datasource.Options{Store: store, Executor: executor, DisableCache: true, ResolveResource: prepared.provider.DatasourceResourceRevalidator, ResolveDefinition: prepared.provider.DatasourceDefinitionResolver, AuthorizeDefinition: prepared.provider.DatasourceDefinitionAuthorize, Authorize: prepared.provider.DatasourceAuthorize})
	opts := datasource.FetchOptions{Resource: result.Data.Resource, Target: result.Data.ResourceTarget}
	rows, err := svc.Fetch(context.Background(), "rows", nil, opts)
	if err != nil || rows == nil || len(rows.Rows) != 1 || executor.seen["targetDefault"] != "phone" || executor.calls != 1 {
		t.Fatalf("approved target pipeline: rows=%+v err=%v calls=%d args=%v", rows, err, executor.calls, executor.seen)
	}
	changed := *opts.Target
	changed.FormFactor = "tablet"
	bad := opts
	bad.Target = &changed
	if rows, err := svc.Fetch(context.Background(), "rows", nil, bad); err == nil || rows != nil || executor.calls != 1 {
		t.Fatal("same parent pin accepted target drift")
	}
	changed = *opts.Target
	changed.SelectionToken = strings.Repeat("x", 43)
	bad.Target = &changed
	if _, err := svc.Fetch(context.Background(), "rows", nil, bad); err == nil || executor.calls != 1 {
		t.Fatal("forged target proof executed")
	}
	executor.during = func() { source.allowed = false }
	if rows, err := svc.Fetch(context.Background(), "rows", nil, opts); err == nil || rows != nil || executor.calls != 2 {
		t.Fatal("revoked parent leaked target rows")
	}
}
