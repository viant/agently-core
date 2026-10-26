//go:build artifactcohortintegration

package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	dssvc "github.com/viant/agently-core/service/datasource"
	"github.com/viant/authz"
	cohort "github.com/viant/forge/backend/artifactcohort"
	"github.com/viant/forge/backend/types"
)

type integrationFactsKey struct{}
type integrationFacts struct{}

func (integrationFacts) Resolve(ctx context.Context) (authz.Facts, error) {
	facts, ok := ctx.Value(integrationFactsKey{}).(authz.Facts)
	if !ok {
		return authz.Facts{}, errors.New("missing verified facts")
	}
	return facts, nil
}

type integrationCaller struct {
	publisher *cohort.Publisher
	fetches   atomic.Int32
}

func (c *integrationCaller) Discover(context.Context) (cohort.Manifest, error) {
	return c.publisher.Snapshot()
}
func (c *integrationCaller) Fetch(_ context.Context, input cohort.FetchInput) (*cohort.Artifact, error) {
	c.fetches.Add(1)
	return c.publisher.Get(input.Root)
}
func TestRealSchema2BundleReplicaBindAndDatasourceRoute(t *testing.T) {
	ctx := context.Background()
	publisher := cohort.NewPublisher()
	descriptor, err := types.CanonicalWindowDescriptor(json.RawMessage(`{"id":"connectors","backend":{"kind":"mcp_tool","service":"studio","method":"sdk.connectors.list"},"selectors":{"data":"items"}}`))
	if err != nil {
		t.Fatal(err)
	}
	variant := types.WindowResourceVariant{Window: &types.Window{WindowKey: "connectors", View: types.View{Content: &types.Container{ID: "root", Binding: types.Binding{DataSourceRef: "connectors"}}}, DataSource: map[string]types.DataSource{"connectors": {}}, ResourceDependencies: map[string]string{"connectors": cohort.Hash(descriptor)}}, DataSources: map[string]json.RawMessage{"connectors": descriptor}}
	fingerprint, err := types.WindowVariantFingerprint(variant)
	if err != nil {
		t.Fatal(err)
	}
	bundle := types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Targets: []types.WindowTargetBinding{{Variant: fingerprint}}, Variants: map[string]types.WindowResourceVariant{fingerprint: variant}}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	published, _, err := publisher.Publish(ctx, cohort.RootRef{URI: "window://datastudio/connectors", Kind: "window", Version: 1}, raw, func(_ context.Context, body json.RawMessage) error {
		_, e := types.SelectWindowResource(body, nil)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	resource := authz.Resource{Kind: "cohort", ID: "operators", Version: "logical", Tenant: "tenant"}
	policy := authz.Document{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{cohort.PolicyAction: {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "operator"}}}}
	member := cohort.Cohort{Tenant: "tenant", Name: "operators", PolicyResource: resource, Roots: []cohort.RootRef{published.Root}}
	if _, err := publisher.ReplaceCatalog(ctx, []authz.Document{policy}, []cohort.Cohort{member}, publisher.Generation()); err != nil {
		t.Fatal(err)
	}
	caller := &integrationCaller{publisher: publisher}
	replica, err := cohort.NewReplica(cohort.Config{Enabled: true, Source: "datastudio", Tenant: "tenant", Account: "acct", Caller: caller, Facts: integrationFacts{}, SessionKey: func(ctx context.Context) (string, error) {
		value := runtimerequestctx.UIBridgeSessionIDFromContext(ctx)
		if value == "" {
			return "", cohort.ErrDenied
		}
		return value, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	actor := context.WithValue(ctx, integrationFactsKey{}, authz.Facts{Subject: "alice", Tenant: "tenant", Issuer: "verified", Roles: []string{"operator"}, ValidUntil: time.Now().Add(time.Hour)})
	actor = context.WithValue(actor, cohortOwnerKey{}, "alice")
	actor = runtimerequestctx.WithUIBridgeSessionID(actor, "session-1")
	if err := replica.Sync(actor); err != nil {
		t.Fatal(err)
	}
	if err := replica.Admit(actor); err != nil {
		t.Fatal(err)
	}
	selected, hash, err := replica.OpenWindowVariant(actor, published.Root.URI, nil)
	if err != nil {
		t.Fatal(err)
	}
	tool := &publishedTool{}
	backend := &backendClient{datasourceSvc: dssvc.New(dssvc.Options{Executor: tool, ReadContext: func(context.Context) (context.Context, error) { t.Fatal("legacy metadata read"); return nil, nil }})}
	opener := func(ctx context.Context, provider, uri string, target *types.WindowTarget) (*types.WindowResourceVariant, string, error) {
		if provider != "datastudio" {
			return nil, "", cohort.ErrDenied
		}
		return replica.OpenWindowVariant(ctx, uri, target)
	}
	owner := func(ctx context.Context) (string, error) {
		value, _ := ctx.Value(cohortOwnerKey{}).(string)
		if value == "" {
			return "", cohort.ErrDenied
		}
		return value, nil
	}
	bindings := NewPublishedWindowBindings(opener, backend.FetchPublishedDatasource, owner)
	if err := bindings.Bind(actor, "client-1", "window-1", "datastudio", published.Root.URI, hash, selected); err != nil {
		t.Fatal(err)
	}
	priorFetches := caller.fetches.Load()
	request := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/connectors/fetch?cohort=1&windowId=window-1", strings.NewReader(`{}`))
	request.SetPathValue("id", "connectors")
	request.Header.Set("Mcp-Session-Id", "session-1")
	request = request.WithContext(actor)
	response := httptest.NewRecorder()
	handleFetchDatasourceWithBindings(backend, bindings)(response, request)
	if response.Code != http.StatusOK || tool.calls != 1 || caller.fetches.Load() != priorFetches || !strings.Contains(response.Body.String(), "real-mcp-tool-row") {
		t.Fatalf("real bundle route: status=%d tools=%d fetches=%d→%d body=%s", response.Code, tool.calls, priorFetches, caller.fetches.Load(), response.Body.String())
	}
	// A second cohort selects a different immutable revision of this same URI.
	// The existing window binding must continue to execute against its exact pin.
	variant.Window.WindowKey = "connectors-preview"
	previewFingerprint, err := types.WindowVariantFingerprint(variant)
	if err != nil {
		t.Fatal(err)
	}
	previewRaw, err := json.Marshal(types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Targets: []types.WindowTargetBinding{{Variant: previewFingerprint}}, Variants: map[string]types.WindowResourceVariant{previewFingerprint: variant}})
	if err != nil {
		t.Fatal(err)
	}
	preview, _, err := publisher.Publish(ctx, cohort.RootRef{URI: published.Root.URI, Kind: "window", Version: 2}, previewRaw, func(_ context.Context, body json.RawMessage) error {
		_, e := types.SelectWindowResource(body, nil)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	previewPolicy := policy
	previewPolicy.Resource.ID = "preview"
	previewPolicy.Policies = map[string]authz.Policy{cohort.PolicyAction: {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "preview"}}}
	if _, err = publisher.ReplaceCatalog(ctx, []authz.Document{policy, previewPolicy}, []cohort.Cohort{member, {Tenant: "tenant", Name: "preview", PolicyResource: previewPolicy.Resource, Roots: []cohort.RootRef{preview.Root}}}, publisher.Generation()); err != nil {
		t.Fatal(err)
	}
	if err = replica.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err = replica.EnsureAdmitted(actor); err != nil {
		t.Fatal(err)
	}
	previewActor := context.WithValue(ctx, integrationFactsKey{}, authz.Facts{Subject: "bob", Tenant: "tenant", Issuer: "verified", Roles: []string{"preview"}, ValidUntil: time.Now().Add(time.Hour)})
	previewActor = context.WithValue(previewActor, cohortOwnerKey{}, "bob")
	previewActor = runtimerequestctx.WithUIBridgeSessionID(previewActor, "session-2")
	if err = replica.Admit(previewActor); err != nil {
		t.Fatal(err)
	}
	previewVariant, previewHash, err := replica.OpenWindowVariant(previewActor, published.Root.URI, nil)
	if err != nil || previewHash == hash || previewVariant.Window.WindowKey != "connectors-preview" {
		t.Fatalf("preview revision selection: hash=%s err=%v", previewHash, err)
	}
	if err = bindings.Bind(previewActor, "client-2", "window-1", "datastudio", published.Root.URI, previewHash, previewVariant); err != nil {
		t.Fatal(err)
	}
	priorFetches = caller.fetches.Load()
	for _, actorContext := range []context.Context{actor, previewActor} {
		request := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/connectors/fetch?cohort=1&windowId=window-1", strings.NewReader(`{}`))
		request.SetPathValue("id", "connectors")
		request.Header.Set("Mcp-Session-Id", runtimerequestctx.UIBridgeSessionIDFromContext(actorContext))
		response := httptest.NewRecorder()
		handleFetchDatasourceWithBindings(backend, bindings)(response, request.WithContext(actorContext))
		if response.Code != http.StatusOK {
			t.Fatalf("coexisting revision binding: status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if tool.calls != 3 || caller.fetches.Load() != priorFetches {
		t.Fatalf("two revision bindings must execute one data call each and no metadata fetch: tools=%d", tool.calls)
	}
}
