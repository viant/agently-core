package datasource_test

import (
	"context"
	"encoding/json"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/reporting"
	"github.com/viant/forge/backend/reporting/registry"
	"github.com/viant/forge/backend/types"
	"strings"
	"sync"
	"testing"
	"time"
)

type ordinaryReportPolicy func(context.Context, identity.ResourceRef, []identity.ResourceCandidate) (identity.ResourceDecision, error)

func (f ordinaryReportPolicy) SelectRevision(ctx context.Context, ref identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	return f(ctx, ref, candidates)
}

func TestOrdinaryMCPCursorReportAggregatesUnderOriginalResourceAndLease(t *testing.T) {
	for _, scenario := range []string{"complete", "complete numeric cursor", "complete object cursor", "complete structured string cursor", "complete structured safe object cursor", "unsupported structured numeric cursor", "unsupported structured fractional scalar cursor", "unsupported structured fractional object cursor", "unsupported structured integer cursor", "unsupported structured object cursor", "revoked after page one", "source drift after page one", "future policy deadline expired after page one", "original held lease expired after page one", "managing MCP denied page two", "authority changed after page two"} {
		t.Run(scenario, func(t *testing.T) {
			fixture, executor, ctx := ordinaryMCPStack(t)
			if strings.Contains(scenario, "structured") {
				fixture.mu.Lock()
				fixture.structured = true
				fixture.mu.Unlock()
			}
			ds := ordinaryDescriptor("widgets", "cursor")
			if scenario == "complete numeric cursor" || scenario == "unsupported structured numeric cursor" || scenario == "unsupported structured fractional scalar cursor" || scenario == "unsupported structured integer cursor" {
				ds.Backend.Method = "cursor-number"
			}
			if scenario == "complete object cursor" || scenario == "unsupported structured object cursor" || scenario == "unsupported structured fractional object cursor" {
				ds.Backend.Method = "cursor-object"
			}
			if scenario == "complete structured safe object cursor" {
				ds.Backend.Method = "cursor-object-safe"
			}
			fixture.mu.Lock()
			if scenario == "unsupported structured fractional scalar cursor" {
				fixture.cursorTokenOverride = json.Number("1.00000000000000001")
			}
			if scenario == "unsupported structured fractional object cursor" {
				fixture.cursorTokenOverride = map[string]any{"ordinal": json.Number("1.00000000000000001"), "tag": "opaque"}
			}
			if scenario == "unsupported structured integer cursor" {
				fixture.cursorTokenOverride = json.Number("42")
			}
			fixture.mu.Unlock()
			ds.Paging = &types.PagingConfig{Enabled: true, Mode: "cursor", Size: 1, Parameters: &types.PagingParameters{Page: "resumeToken", Size: "batchSize"}, DataInfoSelectors: &types.DataInfoSelectors{HasMore: "remaining", NextCursor: "nextToken"}}
			definition, _ := json.Marshal(ds)
			spec := json.RawMessage(`{"version":1,"kind":"reportSpec","source":{"kind":"dashboard.reportBuilder","containerId":"generic","stateKey":"generic","dataSourceRef":"widgets"},"title":"Ordinary Cursor Report","parameters":{"viewMode":"table","groupBy":"","pageSize":1,"orderField":"","orderDir":"asc"},"layoutIntent":{"kind":"single","resultPanePosition":"left","blockOrder":["table"]},"refinements":[],"calculatedFields":[],"datasets":[{"id":"primary","dataSourceRef":"widgets","request":{"term":"report"}}],"blocks":[{"id":"table","kind":"tableBlock","datasetRef":"primary","columns":[{"key":"id","label":"ID"}]}]}`)
			envelope := registry.ReportEnvelope{SchemaVersion: 1, ReportDocument: json.RawMessage(`{}`), ReportSpec: spec, DataSources: map[string]json.RawMessage{"widgets": definition}}
			content, _ := json.Marshal(envelope)
			uri, _ := identity.ParseResourceURI("report://ordinary/cursor")
			var stateMu sync.Mutex
			allowed := true
			authority := "original-account"
			expiry := time.Now().Add(time.Minute)
			clock := time.Now()
			resolver := &identity.ResourceResolver{Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) {
				stateMu.Lock()
				defer stateMu.Unlock()
				return append(json.RawMessage(nil), content...), nil
			}}, Policy: ordinaryReportPolicy(func(_ context.Context, _ identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
				stateMu.Lock()
				defer stateMu.Unlock()
				if !allowed {
					return identity.ResourceDecision{}, identity.ErrResourceDenied
				}
				return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: authority, ValidUntil: expiry}, nil
			})}
			service := reporting.New(reporting.Options{Store: reporting.NewMemoryStore(), Compiler: reporting.NewReportSpecCompiler(time.Now), Now: func() time.Time { stateMu.Lock(); defer stateMu.Unlock(); return clock }, ResourceResolver: func(context.Context, string) (*identity.ResourceResolver, error) { return resolver, nil }})
			service.SetResourceDatasetExecutor(reporting.NewResourceDatasetExecutor(executor, service.AuthorizeResourceDataset))
			fixture.mu.Lock()
			if scenario == "managing MCP denied page two" {
				fixture.denyPage = 2
			}
			fixture.during = func(page int) {
				stateMu.Lock()
				defer stateMu.Unlock()
				if page == 1 {
					switch scenario {
					case "revoked after page one":
						allowed = false
					case "source drift after page one":
						content = append(content, ' ')
					case "future policy deadline expired after page one":
						expiry = time.Now().Add(-time.Second)
					case "original held lease expired after page one":
						clock = expiry.Add(time.Second)
					}
				}
				if page == 2 && scenario == "authority changed after page two" {
					authority = "other-account"
				}
			}
			fixture.mu.Unlock()
			result, err := service.ExecuteResource(ctx, &reporting.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: uri.String()}})
			if strings.HasPrefix(scenario, "complete") {
				if err != nil {
					t.Fatal(err)
				}
				rows := result.Datasets["primary"].Rows
				if !strings.Contains(string(result.ReportFill), "beta") || !strings.Contains(string(result.ReportPrint), "beta") {
					t.Fatal("aggregation did not feed final rendered artifacts")
				}

				if len(rows) != 2 || rows[0]["id"] != "alpha" || rows[1]["id"] != "beta" || len(result.ReportFill) == 0 || len(result.ReportPrint) == 0 {
					t.Fatalf("complete report lost pages or artifacts: %#v", result)
				}
			} else if strings.HasPrefix(scenario, "unsupported structured") {
				if err == nil || result != nil || !strings.Contains(err.Error(), "lossless") {
					t.Fatalf("unsafe structured cursor was not refused before dispatch: %v", err)
				}
			} else if err == nil || result != nil {
				t.Fatal("partial rows/artifacts escaped original resource guard")
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			expected := 2
			// The immutable admitted report stays selected for this operation.
			// Future policy/source changes are verified before release, while
			// the original held deadline and managing MCP denial remain immediate.
			if strings.HasPrefix(scenario, "unsupported structured") || scenario == "original held lease expired after page one" {
				expected = 1
			}
			if len(fixture.calls) != expected {
				t.Fatalf("calls=%d expected=%d error=%v", len(fixture.calls), expected, err)
			}
			if scenario == "managing MCP denied page two" && fixture.deniedCalls != 1 {
				t.Fatalf("managing denial attempts=%d, expected1 with no replay", fixture.deniedCalls)
			}
			for i, call := range fixture.calls {
				if call.Name != ds.Backend.Method || len(call.Args) != 2+i || call.Args["term"] != "report" {
					t.Fatalf("changed tool/inputs across report pages: %#v", fixture.calls)
				}
			}
			if expected == 2 && ds.Backend.Method == "cursor" && fixture.calls[1].Args["resumeToken"] != "after-alpha" {
				t.Fatal("report changed opaque token")
			}
		})
	}
}
