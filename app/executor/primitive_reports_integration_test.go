package executor_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	execconfig "github.com/viant/agently-core/app/executor/config"
	authctx "github.com/viant/agently-core/internal/auth"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
	primitiveprovider "github.com/viant/agently-core/service/primitiveprovider"
	reportingsvc "github.com/viant/agently-core/service/reporting"
	"github.com/viant/agently-core/service/resource/provider"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/forge/backend/reporting/registry"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
)

const reportFixtureToken = "verified-report-fixture-token"
const reportProviderID = "independent-report-provider"
const reportToolService = "independent-report-tool"
const reportURI = "report://example/summary"

type reportActorContextKey struct{}
type reportIntegrationProvider struct{ options map[string]*mcpcfg.MCPClient }

func (p reportIntegrationProvider) Names(context.Context) ([]string, error) {
	names := make([]string, 0, len(p.options))
	for name := range p.options {
		names = append(names, name)
	}
	return names, nil
}
func (p reportIntegrationProvider) Options(_ context.Context, name string) (*mcpcfg.MCPClient, error) {
	v, ok := p.options[name]
	if !ok {
		return nil, fmt.Errorf("unknown report fixture server %s", name)
	}
	return v, nil
}

type reportIntegrationPolicy struct{ provider string }

func (p reportIntegrationPolicy) SelectRevision(ctx context.Context, ref identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	actor, ok := ctx.Value(reportActorContextKey{}).(identity.VerifiedActor)
	if !ok || actor.Subject != "report-user" || len(candidates) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: p.provider + ":" + actor.AccountID, ValidUntil: actor.ValidUntil}, nil
}

type reportToolHTTP struct {
	mu       sync.Mutex
	calls    []map[string]any
	authSeen []string
	methods  []string
}

func (h *reportToolHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+reportFixtureToken {
		http.Error(w, "invalid report fixture credentials", http.StatusUnauthorized)
		return
	}
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad MCP request", 400)
		return
	}
	h.mu.Lock()
	h.methods = append(h.methods, req.Method)
	h.mu.Unlock()
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result any
	switch req.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "report-fixture-session")
		result = map[string]any{"protocolVersion": schema.LatestProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "independent-report-tool", "version": "1"}}
	case "server/discover":
		result = map[string]any{"cacheScope": "private", "capabilities": map[string]any{"tools": map[string]any{}}, "resultType": "complete", "supportedVersions": []string{schema.LatestProtocolVersion}, "ttlMs": 0}
	case "ping":
		result = map[string]any{}
	case "tools/list":
		result = map[string]any{"tools": []any{map[string]any{"name": "queryRows", "description": "Read report rows", "inputSchema": map[string]any{"type": "object", "additionalProperties": true}}}}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if json.Unmarshal(req.Params, &params) != nil || params.Name != "queryRows" {
			result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "invalid tool"}}}
			break
		}
		h.mu.Lock()
		copyArgs := map[string]any{}
		for k, v := range params.Arguments {
			copyArgs[k] = v
		}
		h.calls = append(h.calls, copyArgs)
		h.authSeen = append(h.authSeen, r.Header.Get("Authorization"))
		h.mu.Unlock()
		if account, ok := params.Arguments["accountId"].(string); ok && account != "account" {
			http.Error(w, "foreign account filter denied", http.StatusForbidden)
			return
		}
		offset := 0
		for _, key := range []string{"skipRows", "offset", "Offset", "Page"} {
			if v, ok := params.Arguments[key].(float64); ok {
				offset = int(v)
				if key == "Page" {
					offset = (int(v) - 1) * 2
				}
				break
			}
		}
		rows := []any{}
		all := []map[string]any{{"category": "First", "date": "2000-01-01", "amount": 7, "count": 3}, {"category": "Second", "date": "2000-01-02", "amount": 8, "count": 4}, {"category": "Third", "date": "2000-01-03", "amount": 9, "count": 5}}
		for i := offset; i < len(all) && i < offset+2; i++ {
			rows = append(rows, all[i])
		}
		body := map[string]any{"data": rows, "Data": rows, "metrics": map[string]any{"rowCount": len(rows)}, "meta": map[string]any{"totalCount": 3, "pageCount": 2, "hasMore": offset+2 < 3}, "Meta": map[string]any{"RecordCount": 3, "PageCount": 2}}
		encoded, _ := json.Marshal(body)
		result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(encoded)}}}
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": result})
}

func TestBuilderRunsAuthoredExternalReportThroughPrimitiveHTTPMCP(t *testing.T) {
	runPrimitiveReportFixture(t, "windows")
}

func TestBuilderPrimitiveReportsHeadless(t *testing.T) {
	t.Run("missing proof denies before datasource", func(t *testing.T) { runPrimitiveReportFixture(t, "headless-missing-proof") })
	t.Run("existing proof binds exact provider pins", func(t *testing.T) { runPrimitiveReportFixture(t, "headless-proof") })
}

func runPrimitiveReportFixture(t *testing.T, mode string) {
	t.Setenv("TMPDIR", t.TempDir())
	workspaceRoot := t.TempDir()
	previousRoot, previousRuntimeRoot := workspace.Root(), workspace.RuntimeRoot()
	workspace.SetRoot(workspaceRoot)
	workspace.SetRuntimeRoot(workspaceRoot)
	t.Setenv("AGENTLY_RUNTIME_ROOT", workspaceRoot)
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Cleanup(func() { workspace.SetRoot(previousRoot); workspace.SetRuntimeRoot(previousRuntimeRoot) })
	actor := identity.VerifiedActor{Issuer: "https://identity.fixture", Subject: "report-user", TenantID: "tenant", AccountID: "account", IdentityRevision: "r1", ValidUntil: time.Now().Add(2 * time.Hour)}
	ctx := authctx.WithProvider(authctx.WithCanonicalUserID(authsvc.InjectUser(context.Background(), actor.Subject), actor.Subject), "fixture-oauth")
	ctx = authctxWithReportToken(ctx, reportFixtureToken)
	ctx = context.WithValue(ctx, reportActorContextKey{}, actor)
	ordinary := &reportToolHTTP{}
	ordinaryServer := httptest.NewServer(ordinary)
	t.Cleanup(ordinaryServer.Close)

	raw, err := os.ReadFile(filepath.Join("..", "..", "service", "reporting", "testdata", "synthetic_authored_report.json"))
	require.NoError(t, err)
	var envelope registry.ReportEnvelope
	require.NoError(t, json.Unmarshal(raw, &envelope))
	envelope.SchemaVersion = 2
	envelope.Format = registry.ResourceReportFormat
	envelope.SourceFormat = registry.AuthoredReportFormat
	envelope.DataSourceResources = map[string]primitive.DataSourceReference{}
	providerBindings := make([]provider.LocalResourceBinding, 0, len(envelope.DataSources))
	for id, descriptorRaw := range envelope.DataSources {
		var descriptor dsproto.DataSource
		require.NoError(t, json.Unmarshal(descriptorRaw, &descriptor))
		descriptor.Backend = &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: reportToolService, Method: "queryRows"}
		descriptor.Selectors = &types.Selectors{Data: "data", DataInfo: "meta"}
		descriptor.Parameters = []types.Parameter{{Name: "accountId", Default: "account"}}
		descriptor.Paging = &types.PagingConfig{Mode: "offset", Enabled: true, Size: 2, Parameters: &types.PagingParameters{Page: "skipRows", Size: "takeRows"}, DataInfoSelectors: &types.DataInfoSelectors{HasMore: "hasMore"}}
		descriptor.ResponseAliases = nil
		descriptorRaw, err = json.Marshal(&descriptor)
		require.NoError(t, err)
		envelope.DataSources[id] = descriptorRaw
		wrapped, err := json.Marshal(map[string]any{"schemaVersion": 1, "dataSource": json.RawMessage(descriptorRaw)})
		require.NoError(t, err)
		uri := "datasource://example/" + id
		fingerprint := identity.ContentFingerprint(wrapped)
		envelope.DataSourceResources[id] = primitive.DataSourceReference{Resource: identity.ResourceRef{URI: uri, Revision: identity.WorkingCandidate}, ContentFingerprint: fingerprint, ProviderIdentity: reportProviderID}
		descriptorFingerprint := identity.ContentFingerprint(descriptorRaw)
		for i := range envelope.Dependencies {
			if envelope.Dependencies[i].Kind == "datasource" && envelope.Dependencies[i].ID == id {
				envelope.Dependencies[i].ContentFingerprint = descriptorFingerprint
			}
		}
		resourceURI, parseErr := identity.ParseResourceURI(uri)
		require.NoError(t, parseErr)
		body := append(json.RawMessage(nil), wrapped...)
		providerBindings = append(providerBindings, provider.LocalResourceBinding{URI: uri, Title: id, FormatVersion: 1, Resolver: func(_ context.Context, _ identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
			return &identity.ResourceResolver{ProviderIdentity: reportProviderID, Source: &identity.LocalResource{URI: resourceURI, Load: func(context.Context) (json.RawMessage, error) { return append(json.RawMessage(nil), body...), nil }}, Policy: reportIntegrationPolicy{provider: reportProviderID}}, nil
		}})
	}
	var reportDocument map[string]any
	require.NoError(t, json.Unmarshal(envelope.ReportDocument, &reportDocument))
	blocks, ok := reportDocument["blocks"].([]any)
	require.True(t, ok)
	retainedBlocks := make([]any, 0, len(blocks))
	for _, block := range blocks {
		entry, _ := block.(map[string]any)
		if entry["kind"] != "tableBlock" {
			retainedBlocks = append(retainedBlocks, block)
		}
	}
	require.NotEmpty(t, retainedBlocks)
	reportDocument["blocks"] = retainedBlocks
	envelope.ReportDocument, err = json.Marshal(reportDocument)
	require.NoError(t, err)
	content, err := json.Marshal(&envelope)
	require.NoError(t, err)
	reportIdentity, parseErr := identity.ParseResourceURI(reportURI)
	require.NoError(t, parseErr)
	var reportBodyMu sync.Mutex
	reportBody := append(json.RawMessage(nil), content...)
	providerBindings = append(providerBindings, provider.LocalResourceBinding{URI: reportURI, Title: "Synthetic Summary", FormatVersion: 1, Resolver: func(_ context.Context, _ identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
		return &identity.ResourceResolver{ProviderIdentity: reportProviderID, Source: &identity.LocalResource{URI: reportIdentity, Load: func(context.Context) (json.RawMessage, error) {
			reportBodyMu.Lock()
			defer reportBodyMu.Unlock()
			return append(json.RawMessage(nil), reportBody...), nil
		}}, Policy: reportIntegrationPolicy{provider: reportProviderID}}, nil
	}})

	primitiveProvider, err := provider.NewLocalProvider(provider.LocalConfig{ProviderIdentity: reportProviderID, Actor: func(ctx context.Context) (identity.VerifiedActor, error) {
		a, ok := ctx.Value(reportActorContextKey{}).(identity.VerifiedActor)
		if !ok {
			return identity.VerifiedActor{}, identity.ErrResourceDenied
		}
		return a, nil
	}, Verify: func(_ context.Context, a identity.VerifiedActor) error {
		if !a.Valid(time.Now()) || a.Subject != actor.Subject {
			return identity.ErrResourceDenied
		}
		return nil
	}, Authorize: func(_ context.Context, a identity.VerifiedActor, u identity.ResourceURI, _ string) error {
		if a.Subject != actor.Subject || u.Namespace != "example" {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: providerBindings, Validators: map[string]provider.LocalResourceValidator{"report": func(version int64, raw json.RawMessage) error {
		if version != 1 {
			return identity.ErrResource
		}
		var value map[string]json.RawMessage
		if json.Unmarshal(raw, &value) != nil || len(value) == 0 {
			return identity.ErrResource
		}
		return nil
	}, "datasource": func(version int64, raw json.RawMessage) error {
		if version != 1 {
			return identity.ErrResource
		}
		var value struct {
			SchemaVersion int64           `json:"schemaVersion"`
			DataSource    json.RawMessage `json:"dataSource"`
		}
		if json.Unmarshal(raw, &value) != nil || value.SchemaVersion != 1 {
			return identity.ErrResource
		}
		_, err := types.CanonicalWindowDescriptor(value.DataSource)
		return err
	}}})
	require.NoError(t, err)
	protectedPrimitive, err := provider.NewMCPHandler(primitiveProvider, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+reportFixtureToken {
				http.Error(w, "invalid provider credential", 401)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), reportActorContextKey{}, actor)))
		})
	})
	require.NoError(t, err)
	primitiveServer := httptest.NewServer(protectedPrimitive)
	t.Cleanup(primitiveServer.Close)
	clientOptions := func(url string, providerID string) *mcpcfg.MCPClient {
		pass := true
		return &mcpcfg.MCPClient{PrimitiveProviderIdentity: providerID, ClientOptions: &mcp.ClientOptions{ProtocolVersion: schema.LatestProtocolVersion, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: url + "/mcp"}}, Auth: &mcp.ClientAuth{PassUserToken: &pass, UseIdToken: true}}}
	}
	mcpProvider := reportIntegrationProvider{options: map[string]*mcpcfg.MCPClient{"primitive-source": clientOptions(primitiveServer.URL, reportProviderID), reportToolService: clientOptions(ordinaryServer.URL, "")}}
	mgr, err := manager.New(mcpProvider)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("report-user") })
	verify := func(_ context.Context, a identity.VerifiedActor) error {
		if !a.Valid(time.Now()) || a.Subject != actor.Subject {
			return identity.ErrResourceDenied
		}
		return nil
	}
	proof, err := types.NewWindowTargetHMAC([]byte(strings.Repeat("p", 32)))
	require.NoError(t, err)
	builder := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithMCPManager(mgr).WithPrimitiveAuthority(func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, verify, "local-workspace").WithDefaults(&execconfig.Defaults{Reporting: execconfig.ReportingDefaults{Enabled: true, QueueIntervalMs: 60000}})
	if mode == "windows" {
		builder = builder.WithUIBridge(primitiveprovider.NewService(&primitiveprovider.Config{})).WithPrimitiveWindows(proof)
	} else if mode == "headless-proof" {
		builder = builder.WithPrimitiveReportDependencyProof(proof)
	}
	built, err := builder.Build(ctx)
	require.NoError(t, err)
	defer func() { _ = built.Close(context.Background()) }()
	require.NotNil(t, built.Reporting)
	// The Builder path must install its resource reader and external datasource provider itself.
	phaseStarted := time.Now()
	run, err := built.Reporting.ExecuteResource(ctx, &reportingsvc.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: reportURI}})
	t.Logf("report phase=initial_execute elapsed_ms=%d error=%t", time.Since(phaseStarted).Milliseconds(), err != nil)
	if mode == "headless-missing-proof" {
		require.Error(t, err)
		ordinary.mu.Lock()
		require.Empty(t, ordinary.calls, "missing dependency proof must fail before managing datasource calls")
		ordinary.mu.Unlock()
		return
	}
	if err != nil {
		ordinary.mu.Lock()
		calls, methods := append([]map[string]any(nil), ordinary.calls...), append([]string(nil), ordinary.methods...)
		ordinary.mu.Unlock()
		t.Fatalf("report ExecuteResource failed: %v; ordinary MCP methods=%v tool calls=%v", err, methods, calls)
	}
	require.NotEmpty(t, run.ReportSpec)
	require.NotEmpty(t, run.ReportFill)
	require.NotEmpty(t, run.ReportPrint)
	require.NotEmpty(t, run.Datasets)
	if mode == "headless-proof" {
		require.NotNil(t, run.Resource)
		require.Equal(t, reportProviderID, run.Resource.ProviderIdentity, "the parent report remains pinned to its original external source")
		require.NotEmpty(t, run.DependencyPins)
		require.NotEmpty(t, run.DependencyToken)
		for id, pin := range run.DependencyPins {
			require.Equal(t, reportProviderID, pin.ProviderIdentity)
			require.Equal(t, "datasource://example/"+id, pin.URI, "retain the exact original external datasource URI")
		}
		for id, result := range run.Datasets {
			require.Len(t, result.Rows, 3, "paged datasource %s must aggregate all three actual HTTP MCP rows", id)
		}
		ordinary.mu.Lock()
		before := len(ordinary.calls)
		fixtureCalls := append([]map[string]any(nil), ordinary.calls...)
		fixtureAuth := append([]string(nil), ordinary.authSeen...)
		ordinary.mu.Unlock()
		require.NotEmpty(t, fixtureCalls)
		require.Len(t, fixtureAuth, len(fixtureCalls))
		for _, header := range fixtureAuth {
			require.Equal(t, "Bearer "+reportFixtureToken, header)
		}
		forwardedPageArgs := false
		for _, args := range fixtureCalls {
			if _, ok := args["skipRows"]; ok {
				forwardedPageArgs = true
			}
			if _, ok := args["takeRows"]; ok {
				forwardedPageArgs = true
			}
		}
		require.True(t, forwardedPageArgs, "the authored offset paging arguments must reach the managing HTTP MCP producer")
		forged := make(map[string]identity.ResolvedResource, len(run.DependencyPins))
		for id, pin := range run.DependencyPins {
			forged[id] = pin
		}
		for id, pin := range forged {
			pin.AuthorityBinding = "forged-child-authority"
			forged[id] = pin
			break
		}
		_, forgedErr := built.Reporting.ExecuteResource(ctx, &reportingsvc.ExecuteResourceRequest{ResolvedResource: run.Resource, DependencyPins: forged, DependencyToken: run.DependencyToken})
		require.Error(t, forgedErr)
		_, tokenErr := built.Reporting.ExecuteResource(ctx, &reportingsvc.ExecuteResourceRequest{ResolvedResource: run.Resource, DependencyPins: run.DependencyPins, DependencyToken: run.DependencyToken + "-tampered"})
		require.Error(t, tokenErr)
		ordinary.mu.Lock()
		require.Equal(t, before, len(ordinary.calls), "tampered dependency proof must fail before datasource dispatch")
		ordinary.mu.Unlock()
		return
	}
	for id, result := range run.Datasets {
		require.Len(t, result.Rows, 3, "paged datasource %s must aggregate all three actual HTTP MCP rows", id)
	}
	listed, err := built.Reporting.ListReports(ctx, &reportingsvc.ListReportsInput{Namespace: "example"})
	require.NoError(t, err)
	require.Len(t, listed.Reports, 1)
	require.Equal(t, reportURI, listed.Reports[0].ResourceURI)
	require.NotNil(t, listed.Reports[0].Capabilities)
	require.True(t, listed.Reports[0].Capabilities.Open)
	require.True(t, listed.Reports[0].Capabilities.Run)
	require.True(t, listed.Reports[0].Capabilities.Export)
	require.False(t, listed.Reports[0].Capabilities.Edit)
	require.False(t, listed.Reports[0].Capabilities.Delete)

	ordinary.mu.Lock()
	baselineCalls := len(ordinary.calls)
	ordinary.mu.Unlock()
	_, managingRunErr := built.Reporting.ExecuteResource(ctx, &reportingsvc.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: reportURI}})
	require.NoError(t, managingRunErr, "the managing MCP owns resource execution authorization")
	ordinary.mu.Lock()
	require.Greater(t, len(ordinary.calls), baselineCalls, "actual operation reaches managing datasource")
	ordinary.mu.Unlock()

	// A foreign own-account filter is rejected by the managing MCP endpoint itself.
	ordinary.mu.Lock()
	beforeForeignFilter := len(ordinary.calls)
	ordinary.mu.Unlock()
	phaseStarted = time.Now()
	_, foreignFilterErr := built.Reporting.ExecuteResource(ctx, &reportingsvc.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: reportURI}, Parameters: map[string]interface{}{"accountId": "foreign-fixture-account"}})
	ordinary.mu.Lock()
	foreignCalls := len(ordinary.calls) - beforeForeignFilter
	ordinary.mu.Unlock()
	t.Logf("report phase=managing_deny elapsed_ms=%d backend_calls=%d error=%t", time.Since(phaseStarted).Milliseconds(), foreignCalls, foreignFilterErr != nil)
	require.Error(t, foreignFilterErr)
	require.Contains(t, foreignFilterErr.Error(), "403", "the managing endpoint denial must propagate unchanged")
	ordinary.mu.Lock()
	require.Equal(t, beforeForeignFilter+1, len(ordinary.calls), "the backend receives one rejected request, with no replay")
	ordinary.mu.Unlock()

	// A new operation reads the working definition from its managing provider.
	// An outdated parent pin fails before any dataset dispatch, even without an event.
	reportBodyMu.Lock()
	originalReportBody := append(json.RawMessage(nil), reportBody...)
	reportBody = append(append(json.RawMessage(nil), reportBody...), ' ')
	reportBodyMu.Unlock()
	ordinary.mu.Lock()
	beforeDrift := len(ordinary.calls)
	ordinary.mu.Unlock()
	_, silentErr := built.Reporting.ExecuteResource(ctx, &reportingsvc.ExecuteResourceRequest{ResolvedResource: run.Resource, DependencyPins: run.DependencyPins, DependencyToken: run.DependencyToken})
	require.Error(t, silentErr, "the managing provider changed the selected working definition")
	ordinary.mu.Lock()
	require.Equal(t, beforeDrift, len(ordinary.calls), "stale working definitions must not dispatch a dataset")
	beforeDrift = len(ordinary.calls)
	ordinary.mu.Unlock()
	_, err = mgr.Reconnect(mgr.WithAuthTokenContext(ctx, "primitive-source"), "", "primitive-source")
	require.NoError(t, err)
	_, driftErr := built.Reporting.ExecuteResource(ctx, &reportingsvc.ExecuteResourceRequest{ResolvedResource: run.Resource, DependencyPins: run.DependencyPins, DependencyToken: run.DependencyToken})
	require.Error(t, driftErr)
	ordinary.mu.Lock()
	require.Equal(t, beforeDrift, len(ordinary.calls), "refreshed source drift must fail before datasource dispatch")
	ordinary.mu.Unlock()
	reportBodyMu.Lock()
	reportBody = originalReportBody
	reportBodyMu.Unlock()
	_, err = mgr.Reconnect(mgr.WithAuthTokenContext(ctx, "primitive-source"), "", "primitive-source")
	require.NoError(t, err, "restored producer generation must be rediscovered before later exports")
	ordinary.mu.Lock()
	calls := len(ordinary.calls)
	authSeen := append([]string(nil), ordinary.authSeen...)
	ordinary.mu.Unlock()
	require.Greater(t, calls, 0)
	for _, authHeader := range authSeen {
		require.Equal(t, "Bearer "+reportFixtureToken, authHeader)
	}

	for _, format := range []string{"pdf", "csv", "xlsx"} {
		phaseStarted = time.Now()
		submitted, err := built.Registry.Execute(ctx, "reporting:submit_export", map[string]interface{}{"format": format, "resource": map[string]interface{}{"uri": reportURI}})
		t.Logf("report phase=submit_export format=%s elapsed_ms=%d error=%t", format, time.Since(phaseStarted).Milliseconds(), err != nil)
		require.NoError(t, err)
		var job reportingsvc.ExportJobStatus
		require.NoError(t, json.Unmarshal([]byte(submitted), &job))
		require.Equal(t, reportingsvc.JobStatusQueued, job.Status)
		phaseStarted = time.Now()
		finished, err := built.Reporting.RunExport(ctx, job.JobID)
		t.Logf("report phase=run_export format=%s elapsed_ms=%d error=%t", format, time.Since(phaseStarted).Milliseconds(), err != nil)
		require.NoError(t, err)
		require.Equal(t, reportingsvc.JobStatusSucceeded, finished.Status)
		artifact, err := built.Reporting.GetArtifact(ctx, finished.ArtifactID)
		require.NoError(t, err)
		require.NotEmpty(t, artifact.Data)
		switch format {
		case "pdf":
			require.True(t, strings.HasPrefix(string(artifact.Data), "%PDF-"))
		case "xlsx":
			require.True(t, strings.HasPrefix(string(artifact.Data), "PK\x03\x04"))
		case "csv":
			require.Contains(t, string(artifact.Data), "First,7,3,10")
		}
	}
}

func authctxWithReportToken(ctx context.Context, token string) context.Context {
	return authctx.WithIDToken(ctx, token)
}
