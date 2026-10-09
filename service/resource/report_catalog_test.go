package resource

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/viant/agently-core/service/policy"
	"github.com/viant/authz"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	reporting "github.com/viant/agently-core/service/reporting"
	reportcatalog "github.com/viant/agently-core/service/reporting/catalog"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

type gatewayReportCompiler func(context.Context, *reporting.CompileRequest) (*reporting.CompileResult, error)

func (f gatewayReportCompiler) Compile(ctx context.Context, request *reporting.CompileRequest) (*reporting.CompileResult, error) {
	return f(ctx, request)
}

type boundedReportFixturePolicy struct {
	actor identity.VerifiedActor
	until time.Time
}

func (p boundedReportFixturePolicy) SelectRevision(ctx context.Context, ref identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	decision, err := (localFixturePolicy{actor: p.actor}).SelectRevision(ctx, ref, candidates)
	if err != nil {
		return decision, err
	}
	if p.until.Before(decision.ValidUntil) {
		decision.ValidUntil = p.until
	}
	return decision, nil
}
func reportGatewayFixture(t *testing.T) (*ReportGatewayCatalog, *localFixtureSource, *identity.VerifiedActor, *atomic.Bool, *LocalProvider) {
	allowed := new(atomic.Bool)
	allowed.Store(true)
	until := time.Now().Add(time.Minute)
	source := &localFixtureSource{raw: json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Approved"},"reportSpec":{"kind":"reportSpec","title":"Approved","datasets":[],"blocks":[]}}`)}
	actor := &identity.VerifiedActor{Subject: "alice", Issuer: "https://synthetic.invalid", TenantID: "test", AccountID: "account", IdentityRevision: "r1", ValidUntil: time.Now().Add(time.Hour)}
	actorFn := func(context.Context) (identity.VerifiedActor, error) { return *actor, nil }
	verify := func(_ context.Context, a identity.VerifiedActor) error {
		if !sameLocalActor(a, *actor) {
			return identity.ErrResourceDenied
		}
		return nil
	}
	provider, err := NewLocalProvider(LocalConfig{ProviderIdentity: "internal", Actor: actorFn, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error {
		if !allowed.Load() {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: []LocalResourceBinding{{URI: "report://steward/sales", Title: "Approved", FormatVersion: 1, Resolver: func(_ context.Context, a identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
		return &identity.ResourceResolver{ProviderIdentity: "internal", Source: source, Policy: boundedReportFixturePolicy{actor: a, until: until}}, nil
	}}}, Validators: map[string]LocalResourceValidator{"report": ValidateReportEnvelope}})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	require.NoError(t, mgr.RegisterLocal(context.Background(), "internal", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "internal"}, func(context.Context) (mcpclient.Interface, error) { return NewLocalMCPClient(provider) }))
	gateway := NewGateway(mgr, actorFn, verify, "aggregate")
	t.Cleanup(gateway.Close)
	return &ReportGatewayCatalog{Gateway: gateway, Admission: func(context.Context, string, identity.ResolvedResource, json.RawMessage) error { return nil }}, source, actor, allowed, provider
}
func TestReportingServiceUsesGatewayForListGetCompileAndRechecks(t *testing.T) {
	ctx := context.Background()
	catalog, source, _, _, _ := reportGatewayFixture(t)
	var deny atomic.Bool
	catalog.Admission = func(_ context.Context, operation string, _ identity.ResolvedResource, _ json.RawMessage) error {
		if operation == "report.compile" && deny.Load() {
			return identity.ErrResourceDenied
		}
		return nil
	}
	calls := 0
	drift := false
	service := reporting.New(reporting.Options{Store: reporting.NewMemoryStore(), ReportCatalog: catalog, Compiler: gatewayReportCompiler(func(_ context.Context, request *reporting.CompileRequest) (*reporting.CompileResult, error) {
		calls++
		require.JSONEq(t, `{"kind":"reportSpec","title":"Approved","datasets":[],"blocks":[]}`, string(request.Document))
		if drift {
			source.mu.Lock()
			source.raw = json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Changed"},"reportSpec":{"title":"Changed"}}`)
			source.mu.Unlock()
		}
		return &reporting.CompileResult{ReportSpec: request.Document}, nil
	})})
	service.SetResourceReaderFactory(catalog.Reader)
	listed, err := service.ListReports(ctx, &reporting.ListReportsInput{Namespace: "steward"})
	require.NoError(t, err)
	require.Len(t, listed.Reports, 1)
	original := *listed.Reports[0].Resource
	require.Equal(t, "internal", original.ProviderIdentity)
	got, err := service.GetReport(ctx, &reporting.GetReportInput{ResolvedResource: &original})
	require.NoError(t, err)
	require.Equal(t, original.AuthorityBinding, got.Resource.AuthorityBinding)
	require.False(t, got.Resource.ValidUntil.After(original.ValidUntil))
	compiled, err := service.Compile(ctx, &reporting.CompileRequest{ResolvedResource: got.Resource, Document: json.RawMessage(`{"forged":true}`)})
	require.NoError(t, err)
	require.Equal(t, original.ProviderIdentity, compiled.Resource.ProviderIdentity)
	require.Equal(t, original.ResourceCandidate, compiled.Resource.ResourceCandidate)
	require.Equal(t, original.AuthorityBinding, compiled.Resource.AuthorityBinding)
	require.False(t, compiled.Resource.ValidUntil.After(original.ValidUntil))
	require.Equal(t, 1, calls)
	deny.Store(true)
	result, err := service.Compile(ctx, &reporting.CompileRequest{ResolvedResource: compiled.Resource})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.Nil(t, result)
	require.Equal(t, 1, calls)
	deny.Store(false)
	drift = true
	result, err = service.Compile(ctx, &reporting.CompileRequest{ResolvedResource: compiled.Resource})
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, 2, calls)
	got, err = service.GetReport(ctx, &reporting.GetReportInput{ResolvedResource: &original})
	require.Error(t, err)
	require.Nil(t, got)
}
func TestReportGatewayFinalAdmissionCannotExtendExpiredLeaseOrChangeActor(t *testing.T) {
	for _, scenario := range []string{"lease", "actor"} {
		t.Run(scenario, func(t *testing.T) {
			catalog, _, actor, _, _ := reportGatewayFixture(t)
			now := time.Now()
			catalog.Gateway.Now = func() time.Time { return now }
			calls := 0
			catalog.Admission = func(_ context.Context, _ string, pin identity.ResolvedResource, raw json.RawMessage) error {
				calls++
				if calls == 2 {
					if scenario == "lease" {
						now = pin.ValidUntil.Add(time.Nanosecond)
						// Actor remains valid beyond the original shorter provider lease.
					} else {
						actor.IdentityRevision = "revoked-r2"
					}
				}
				return nil
			}
			reader, err := catalog.Reader(context.Background(), "report.compile")
			require.NoError(t, err)
			pin, err := reader.Resolve(context.Background(), identity.ResourceRef{URI: "report://steward/sales"})
			require.ErrorIs(t, err, identity.ErrResourceDenied)
			require.Nil(t, pin)
		})
	}
}

func TestReportGatewayFinalAdmissionCannotReleaseRevokedProvider(t *testing.T) {
	catalog, _, _, allowed, _ := reportGatewayFixture(t)
	calls := 0
	catalog.Admission = func(context.Context, string, identity.ResolvedResource, json.RawMessage) error {
		calls++
		if calls == 2 {
			allowed.Store(false)
		}
		return nil
	}
	reader, err := catalog.Reader(context.Background(), "report.compile")
	require.NoError(t, err)
	pin, err := reader.Resolve(context.Background(), identity.ResourceRef{URI: "report://steward/sales"})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.Nil(t, pin)
}
func TestReportGatewayCatalogueCapabilitiesCannotReleaseDrift(t *testing.T) {
	catalog, source, _, _, _ := reportGatewayFixture(t)
	catalog.Capabilities = func(context.Context, identity.VerifiedActor, reportcatalog.ReportCatalogCandidate, identity.ResolvedResource) (reportcatalog.ReportCapabilities, error) {
		source.mu.Lock()
		source.raw = json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Changed"},"reportSpec":{"title":"Changed"}}`)
		source.mu.Unlock()
		return reportcatalog.ReportCapabilities{Open: true}, nil
	}
	result, err := catalog.List(context.Background(), reportcatalog.ReportCatalogQuery{Namespace: "steward"})
	require.Error(t, err)
	require.Nil(t, result)
}

func TestReportGatewayMetadataFinishCannotReleaseExpiredPins(t *testing.T) {
	catalog, _, _, _, _ := reportGatewayFixture(t)
	now := time.Now()
	catalog.Gateway.Now = func() time.Time { return now }
	var expires time.Time
	catalog.Admission = func(_ context.Context, _ string, pin identity.ResolvedResource, _ json.RawMessage) error {
		expires = pin.ValidUntil
		return nil
	}
	catalog.BeginMetadataRead = func(ctx context.Context) (context.Context, func() error, error) {
		return ctx, func() error { now = expires.Add(time.Nanosecond); return nil }, nil
	}
	result, err := catalog.List(context.Background(), reportcatalog.ReportCatalogQuery{Namespace: "steward"})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.Nil(t, result)
}

func TestReportGatewayListHidesOrdinaryDenialButRejectsIdentityAndOutages(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		failure error
		hide    bool
	}{{"policy-permission", policy.ErrDenied, true}, {"authz-permission", authz.ErrDenied, true}, {"identity", policy.ErrIdentityRejected, false}, {"identity-joined-denial", errors.Join(identity.ErrResourceDenied, authz.ErrIdentityDenied), false}, {"outage", errors.New("host report policy unavailable"), false}} {
		t.Run(scenario.name, func(t *testing.T) {
			catalog, _, _, _, provider := reportGatewayFixture(t)
			hidden := provider.bindings["report://steward/sales"]
			hidden.URI = "report://steward/hidden"
			hidden.Title = "Hidden"
			provider.bindings[hidden.URI] = hidden
			catalog.Admission = func(_ context.Context, _ string, pin identity.ResolvedResource, _ json.RawMessage) error {
				if pin.URI == hidden.URI {
					return scenario.failure
				}
				return nil
			}
			result, err := catalog.List(context.Background(), reportcatalog.ReportCatalogQuery{Namespace: "steward"})
			if scenario.hide {
				require.NoError(t, err)
				require.Len(t, result.Reports, 1)
				require.Equal(t, "report://steward/sales", result.Reports[0].URI)
			} else {
				require.ErrorIs(t, err, scenario.failure)
				require.Nil(t, result)
			}
		})
	}
}
