package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authsvc "github.com/viant/agently-core/service/auth"
	"github.com/viant/authz"
	"github.com/viant/forge/backend/artifactcohort"
	primitive "github.com/viant/forge/backend/provider/contract"
	"github.com/viant/forge/backend/reporting/registry"
	identity "github.com/viant/forge/backend/resource"
	resources "github.com/viant/forge/backend/resourcehost"
	"github.com/viant/forge/backend/types"
	iauth "github.com/viant/mcp/client/auth/link"
	manager "github.com/viant/mcp/client/pool"
)

type publishedPinCaller struct{ publisher *artifactcohort.Publisher }

func (c publishedPinCaller) Discover(context.Context) (artifactcohort.Manifest, error) {
	return c.publisher.Snapshot()
}
func (c publishedPinCaller) Fetch(_ context.Context, in artifactcohort.FetchInput) (*artifactcohort.Artifact, error) {
	return c.publisher.Get(in.Root)
}

type publishedPinFacts struct{}
type publishedPinSessionKey struct{}

func (publishedPinFacts) Resolve(ctx context.Context) (authz.Facts, error) {
	subject := authsvc.EffectiveUserID(ctx)
	if subject == "" {
		return authz.Facts{}, authz.ErrDenied
	}
	return authz.Facts{Subject: subject, Issuer: "verified", Tenant: "example-tenant", Roles: []string{"reader"}, ValidUntil: time.Now().Add(time.Hour)}, nil
}

type publishedPinFixture struct {
	service   *Service
	replica   *artifactcohort.Replica
	publisher *artifactcohort.Publisher
	policies  []authz.Document
	cohorts   []artifactcohort.Cohort
	calls     int
}

func newPublishedPinFixture(t *testing.T, account string) *publishedPinFixture {
	t.Helper()
	f := &publishedPinFixture{publisher: artifactcohort.NewPublisher()}
	policy := authz.Resource{Kind: "cohort", ID: "readers", Version: "logical", Tenant: "example-tenant"}
	f.policies = []authz.Document{{Resource: policy, Revision: 1, Policies: map[string]authz.Policy{artifactcohort.PolicyAction: {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}
	named := authz.Resource{Kind: "policy", ID: "named-readers", Version: "logical", Tenant: "example-tenant"}
	f.policies = append(f.policies, authz.Document{Resource: named, Revision: 1, Policies: map[string]authz.Policy{authz.NamedDefinitionAction: {Mode: "definition", Definition: &authz.NamedDefinition{DisplayName: "Readers", Policies: map[string]authz.Policy{artifactcohort.PolicyAction: {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}}})
	f.policies[0].Policies[artifactcohort.PolicyAction] = authz.Policy{Mode: "named", Named: &authz.NamedReference{Resource: named, Revision: 1}}
	f.cohorts = []artifactcohort.Cohort{{Tenant: "example-tenant", Name: "readers", PolicyResource: policy}}
	f.publish(t, 1, "Example")
	var err error
	f.replica, err = artifactcohort.NewReplica(artifactcohort.Config{Enabled: true, Source: "publisher", Tenant: "example-tenant", Account: account, Caller: publishedPinCaller{f.publisher}, Facts: publishedPinFacts{}, SessionKey: func(ctx context.Context) (string, error) {
		if binding, ok := ctx.Value(publishedPinSessionKey{}).(string); ok {
			return binding, nil
		}
		return authsvc.EffectiveUserID(ctx), nil
	}})
	require.NoError(t, err)
	require.NoError(t, f.replica.Sync(context.Background()))
	source, err := artifactcohort.NewMetadataSource(f.replica, []string{"example"})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(mgr, actor, verify, "host")
	t.Cleanup(gateway.Close)
	require.NoError(t, gateway.RegisterPublishedMetadataSource(resources.Connection{Name: "catalog", ProviderIdentity: "publisher"}, []string{"example"}, source))
	catalog := &resources.ReportGatewayCatalog{Gateway: gateway}
	f.service = New(Options{Store: NewMemoryStore(), Compiler: NewReportSpecCompiler(nil), Exporter: NewForgeExporter(nil), ReportCatalog: catalog})
	f.service.SetResourceReaderFactory(catalog.Reader)

	signer, err := types.NewWindowTargetHMAC(make([]byte, 32))
	require.NoError(t, err)
	f.service.SetResourceDependencies(func(ctx context.Context, pin identity.ResolvedResource, refs map[string]primitive.DataSourceReference, descriptors map[string]json.RawMessage, original map[string]identity.ResolvedResource) (map[string]identity.ResolvedResource, error) {
		parsed, err := identity.ParseResourceURI(pin.URI)
		if err != nil {
			return nil, err
		}
		parent, err := gateway.ConnectionForResource(ctx, parsed, pin.ProviderIdentity)
		if err != nil {
			return nil, err
		}
		return gateway.ResolveDependencyPins(ctx, parent, pin, refs, descriptors, original)
	}, signer)
	f.service.SetResourceDatasetExecutor(NewResourceDatasetExecutor(reportFixtureTransport(func(context.Context, string, map[string]interface{}) (string, error) {
		f.calls++
		return `{"data":[{"label":"Example","value":7}]}`, nil
	}), f.service.AuthorizeResourceDataset))
	return f
}
func (f *publishedPinFixture) publish(t *testing.T, version uint64, title string, dataMethod ...string) {
	t.Helper()
	method := "read"
	if len(dataMethod) > 0 {
		method = dataMethod[0]
	}
	descriptor := json.RawMessage(fmt.Sprintf(`{"id":"rows","cardinality":"collection","selectors":{"data":"data"},"backend":{"kind":"mcp_tool","service":"data","method":%q}}`, method))
	spec := json.RawMessage(`{"version":1,"kind":"reportSpec","source":{"kind":"report","containerId":"example","stateKey":"example","dataSourceRef":"rows"},"title":"` + title + `","parameters":{"viewMode":"table","groupBy":"","pageSize":2,"orderField":"","orderDir":"asc"},"layoutIntent":{"kind":"stack","resultPanePosition":"bottom","blockOrder":["details"]},"refinements":[],"calculatedFields":[],"datasets":[{"id":"primary","dataSourceRef":"rows","request":{"limit":2,"offset":0}}],"blocks":[{"id":"details","kind":"tableBlock","title":"Details","datasetRef":"primary","columns":[{"key":"label","label":"Label"},{"key":"value","label":"Value"}]}]}`)
	child := json.RawMessage(`{"schemaVersion":1,"dataSource":` + string(descriptor) + `}`)
	raw, err := json.Marshal(registry.ReportEnvelope{SchemaVersion: 2, Format: registry.ResourceReportFormat, ReportSpec: spec, DataSources: map[string]json.RawMessage{"rows": descriptor}, DataSourceResources: map[string]primitive.DataSourceReference{"rows": {Resource: identity.ResourceRef{URI: "datasource://example/rows", Revision: identity.WorkingCandidate}, ProviderIdentity: "publisher", ContentFingerprint: identity.ContentFingerprint(child)}}})
	require.NoError(t, err)
	item, _, err := f.publisher.Publish(context.Background(), artifactcohort.RootRef{URI: "report://example/summary", Kind: "report", Version: version}, raw, func(context.Context, json.RawMessage) error { return nil })
	require.NoError(t, err)
	f.cohorts[0].Roots = []artifactcohort.RootRef{item.Root}
	f.replace(t)
}
func (f *publishedPinFixture) replace(t *testing.T) {
	t.Helper()
	_, err := f.publisher.ReplaceCatalog(context.Background(), f.policies, f.cohorts, f.publisher.Generation())
	require.NoError(t, err)
}
func (f *publishedPinFixture) get(t *testing.T, ctx context.Context) *identity.ResolvedResource {
	t.Helper()
	get, err := f.service.Method("get_report")
	require.NoError(t, err)
	report := &SharedArtifact{}
	require.NoError(t, get(ctx, &GetReportInput{Resource: &identity.ResourceRef{URI: "report://example/summary"}}, report))
	require.True(t, report.Resource.ValidUntil.IsZero())
	raw, err := json.Marshal(report.Resource)
	require.NoError(t, err)
	var pin identity.ResolvedResource
	require.NoError(t, json.Unmarshal(raw, &pin))
	return &pin
}
func (f *publishedPinFixture) run(ctx context.Context, pin *identity.ResolvedResource) error {
	raw, _ := json.Marshal(map[string]any{"resource": identity.ResourceRef{URI: pin.URI}, "resolvedResource": pin, "parameters": map[string]any{}, "fillOnly": true})
	var request ExecuteResourceRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return err
	}
	run, err := f.service.Method("run_report")
	if err != nil {
		return err
	}
	return run(ctx, &request, &ExecuteResourceResult{})
}

func TestPublishedReportRequiresFreshPinAfterSameSubjectCredentialRotation(t *testing.T) {
	f := newPublishedPinFixture(t, "account-one")
	owner := authsvc.InjectUser(context.Background(), "reader")
	before := context.WithValue(owner, publishedPinSessionKey{}, "verified-scope-before")
	after := context.WithValue(owner, publishedPinSessionKey{}, "verified-scope-after")
	original := f.get(t, before)
	fresh := f.get(t, after)
	require.Equal(t, original.ResourceCandidate, fresh.ResourceCandidate)
	require.NotEqual(t, original.AuthorityBinding, fresh.AuthorityBinding)

	require.ErrorIs(t, f.run(after, original), identity.ErrResourceDenied)
	require.Zero(t, f.calls)
	require.NoError(t, f.run(after, fresh))
	require.Equal(t, 1, f.calls)
}
func TestPublishedGetReportPinReplaysAfterUnchangedReconnect(t *testing.T) {
	f := newPublishedPinFixture(t, "account-one")
	ctx := authsvc.InjectUser(context.Background(), "reader")
	pin := f.get(t, ctx)
	require.NoError(t, f.run(ctx, pin))
	before := f.calls
	f.replica.MarkDisconnected()
	require.Error(t, f.run(ctx, pin))
	require.Equal(t, before, f.calls)
	require.NoError(t, f.replica.Sync(ctx))
	require.NoError(t, f.run(ctx, pin))
	require.Equal(t, before+1, f.calls)
	fresh := f.get(t, ctx)
	require.Equal(t, pin.AuthorityBinding, fresh.AuthorityBinding)
	require.Equal(t, pin.ContentFingerprint, fresh.ContentFingerprint)
	before = f.calls
	other := authsvc.InjectUser(context.Background(), "other-reader")
	require.ErrorIs(t, f.run(other, pin), identity.ErrResourceDenied)
	require.Equal(t, before, f.calls)
	anotherAccount := newPublishedPinFixture(t, "account-two")
	require.ErrorIs(t, anotherAccount.run(ctx, pin), identity.ErrResourceDenied)
	require.Zero(t, anotherAccount.calls)
	require.NoError(t, f.run(other, f.get(t, other)), "the other reader has its own valid grant")
	require.NoError(t, anotherAccount.run(ctx, anotherAccount.get(t, ctx)), "the other account has its own valid grant")
	before = f.calls
	for _, kind := range []string{"report", "navigation"} {
		raw := json.RawMessage(`{"schemaVersion":1,"reportSpec":{"version":1,"kind":"reportSpec","title":"Other","datasets":[],"blocks":[]}}`)
		if kind == "navigation" {
			raw = json.RawMessage(`{"schemaVersion":1,"applications":[{"id":"other","menus":[{"id":"other","action":{"type":"report","resourceUri":"report://example/other"}}]}]}`)
		}
		extra, _, err := f.publisher.Publish(ctx, artifactcohort.RootRef{URI: kind + "://example/other", Kind: kind, Version: 1}, raw, func(context.Context, json.RawMessage) error { return nil })
		require.NoError(t, err)
		f.cohorts[0].Roots = append(f.cohorts[0].Roots, extra.Root)
	}
	f.policies[0].Revision++
	f.policies[0].Resource.ID = "new-release-wrapper"
	f.cohorts[0].Name = "new-release"
	f.cohorts[0].PolicyResource = f.policies[0].Resource
	f.replace(t)
	require.NoError(t, f.replica.Sync(ctx))
	require.NoError(t, f.run(ctx, pin))
	require.Equal(t, pin.AuthorityBinding, f.get(t, ctx).AuthorityBinding)
	before = f.calls
	f.policies[1].Revision++
	f.replace(t)
	require.NoError(t, f.replica.Sync(ctx))
	require.Error(t, f.run(ctx, pin))
	require.Equal(t, before, f.calls)
	pin = f.get(t, ctx)
	require.NoError(t, f.run(ctx, pin))
	before = f.calls
	f.publish(t, 2, "Changed")
	require.NoError(t, f.replica.Sync(ctx))
	require.Error(t, f.run(ctx, pin))
	require.Equal(t, before, f.calls)
	changed := f.get(t, ctx)
	require.NoError(t, f.run(ctx, changed))
	before = f.calls
	f.publish(t, 3, "Changed", "readNext")
	require.NoError(t, f.replica.Sync(ctx))
	require.Error(t, f.run(ctx, changed))
	require.Equal(t, before, f.calls)
	changed = f.get(t, ctx)
	require.NoError(t, f.run(ctx, changed))
	before = f.calls
	f.policies[0].Revision++
	f.policies[0].Policies[artifactcohort.PolicyAction] = authz.Policy{Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "administrator"}}
	f.replace(t)
	require.NoError(t, f.replica.Sync(ctx))
	require.Error(t, f.run(ctx, changed))
	require.Equal(t, before, f.calls)
}

// This exercises the public reporting tool contract with an independently
// published closed report and an ordinary reader admitted by its producer.
func TestPublishedClosedRootReaderExportsCSV(t *testing.T) {
	f := newPublishedPinFixture(t, "account-one")
	ctx := iauth.WithCanonicalUserID(authsvc.InjectUser(context.Background(), "reader"), "reader")
	pin := f.get(t, ctx)
	require.NoError(t, f.run(ctx, pin), "the same reader can render this closed report")
	before := f.calls
	submit, err := f.service.Method("submit_export")
	require.NoError(t, err)
	var job ExportJobStatus
	err = submit(ctx, &SubmitExportRequest{Resource: &identity.ResourceRef{URI: pin.URI}, ResolvedResource: pin, Format: ExportFormatCSV, TableBlockID: "details"}, &job)
	require.NoError(t, err, "an admitted closed root must use its embedded datasource contract")
	require.Greater(t, f.calls, before, "export must execute producer data")
	require.NotEmpty(t, job.JobID)

	completed, err := f.service.RunExport(ctx, job.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, completed.Status)
	artifact, err := f.service.GetArtifact(ctx, completed.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, "Label,Value\nExample,7\n", string(artifact.Data))

	for _, scenario := range []string{"child pins", "child proof", "expired pin", "foreign provider"} {
		t.Run(scenario, func(t *testing.T) {
			changed := *pin
			request := &SubmitExportRequest{Resource: &identity.ResourceRef{URI: pin.URI}, ResolvedResource: &changed, Format: ExportFormatCSV, TableBlockID: "details"}
			switch scenario {
			case "child pins":
				request.DependencyPins = map[string]identity.ResolvedResource{"rows": *pin}
			case "child proof":
				request.DependencyToken = "forged"
			case "expired pin":
				changed.ValidUntil = time.Now().Add(-time.Minute)
			case "foreign provider":
				changed.ProviderIdentity = "foreign"
			}
			before := f.calls
			_, err := f.service.SubmitExport(ctx, request)
			require.Error(t, err)
			require.Equal(t, before, f.calls, "denied export must not execute data")
		})
	}
	foreign := iauth.WithCanonicalUserID(authsvc.InjectUser(context.Background(), "other-reader"), "other-reader")
	_, err = f.service.GetArtifact(foreign, completed.ArtifactID)
	require.ErrorIs(t, err, ErrNotFound)
	f.policies[0].Revision++
	f.policies[0].Policies[artifactcohort.PolicyAction] = authz.Policy{Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "administrator"}}
	f.replace(t)
	require.NoError(t, f.replica.Sync(ctx))
	_, err = f.service.GetArtifact(ctx, completed.ArtifactID)
	require.ErrorIs(t, err, ErrNotFound, "producer revocation must deny completed download")
	_, err = f.service.SubmitExport(ctx, &SubmitExportRequest{Resource: &identity.ResourceRef{URI: pin.URI}, ResolvedResource: pin, Format: ExportFormatCSV, TableBlockID: "details"})
	require.Error(t, err, "producer revocation must deny a new export")
}
