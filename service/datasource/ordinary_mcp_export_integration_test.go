package datasource_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	pdfreader "github.com/ledongthuc/pdf"
	iauth "github.com/viant/agently-core/internal/auth"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/reporting"
	"github.com/viant/forge/backend/reporting/registry"
	"github.com/viant/forge/backend/types"
	"github.com/xuri/excelize/v2"
)

// Exercise the production submit -> execute -> materialize -> worker -> CSV
// chain with the independent HTTP MCP server, including its strict tool schema.
func TestOrdinaryMCPUnifiedExportIncludesEveryPageAndRejectsPartialPersistence(t *testing.T) {
	for _, scenario := range []string{"cursor", "offset", "page", "unpaged", "cursor pdf", "cursor xlsx", "offset xlsx", "page xlsx", "unpaged xlsx", "tampered request after submit", "tampered request after submit pdf", "tampered rows after submit", "revoked after rendering", "expired after rendering", "drift after rendering", "unknown completeness", "repeat", "emptyMore", "pageError", "revoked", "drift", "expired", "cancelled", "managing MCP denied page two"} {
		t.Run(scenario, func(t *testing.T) {
			format := reporting.ExportFormatCSV
			if strings.HasSuffix(scenario, " xlsx") {
				format = reporting.ExportFormatXLSX
				scenario = strings.TrimSuffix(scenario, " xlsx")
			}
			if strings.HasSuffix(scenario, " pdf") {
				format = reporting.ExportFormatPDF
				scenario = strings.TrimSuffix(scenario, " pdf")
			}
			fixture, executor, trusted := ordinaryMCPStack(t)
			ctx, cancel := context.WithCancel(trusted)
			defer cancel()
			method, request, field := "cursor", `{"term":"export"}`, "id"
			if scenario == "offset" || scenario == "page" {
				method, request = scenario, `{"term":"explicitEOF"}`
			}
			if scenario == "unpaged" {
				method, field = "lookup", "code"
			}
			if scenario == "repeat" || scenario == "emptyMore" || scenario == "pageError" {
				request = `{"term":"` + scenario + `"}`
			}
			descriptor := ordinaryDescriptor("widgets", method)
			if scenario != "unpaged" {
				descriptor.Paging = &types.PagingConfig{Enabled: true, Mode: "cursor", Size: 1, Parameters: &types.PagingParameters{Page: "resumeToken", Size: "batchSize"}, DataInfoSelectors: &types.DataInfoSelectors{HasMore: "remaining", NextCursor: "nextToken"}}
			}
			if scenario == "offset" || scenario == "page" {
				descriptor.Paging = &types.PagingConfig{Enabled: true, Mode: "offset", Size: 2, Parameters: &types.PagingParameters{Page: "skipRows", Size: "takeRows"}, DataInfoSelectors: &types.DataInfoSelectors{HasMore: "remaining"}}
			}
			if scenario == "page" {
				descriptor.Paging.Mode = "page"
				descriptor.Paging.Parameters.Page = "pageIndex"
			}
			if scenario == "unknown completeness" {
				descriptor.Paging.DataInfoSelectors.HasMore = "missing"
			}
			descriptorBytes, err := json.Marshal(descriptor)
			if err != nil {
				t.Fatal(err)
			}
			spec := json.RawMessage(`{"version":1,"kind":"reportSpec","source":{"kind":"dashboard.reportBuilder","containerId":"generic","stateKey":"generic","dataSourceRef":"widgets"},"title":"Independent Export","parameters":{"viewMode":"table","groupBy":"","pageSize":1,"orderField":"","orderDir":"asc"},"layoutIntent":{"kind":"single","resultPanePosition":"left","blockOrder":["table"]},"refinements":[],"calculatedFields":[],"datasets":[{"id":"primary","dataSourceRef":"widgets","request":` + request + `}],"blocks":[{"id":"table","kind":"tableBlock","datasetRef":"primary","columns":[{"key":"` + field + `","label":"ID"}]}]}`)
			envelope := registry.ReportEnvelope{SchemaVersion: 1, ReportDocument: json.RawMessage(`{}`), ReportSpec: spec, DataSources: map[string]json.RawMessage{"widgets": descriptorBytes}}
			content, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			uri, _ := identity.ParseResourceURI("report://independent/export")
			var mu sync.Mutex
			allowed, expiry := true, time.Now().Add(time.Minute)
			resolver := &identity.ResourceResolver{Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) {
				mu.Lock()
				defer mu.Unlock()
				return append(json.RawMessage(nil), content...), nil
			}}, Policy: ordinaryReportPolicy(func(_ context.Context, _ identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
				mu.Lock()
				defer mu.Unlock()
				if !allowed {
					return identity.ResourceDecision{}, identity.ErrResourceDenied
				}
				return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: "ordinary-owner", ValidUntil: expiry}, nil
			})}
			store := reporting.NewMemoryStore()
			exporter := reporting.NewForgeExporter(nil)
			if strings.HasSuffix(scenario, " after rendering") {
				exporter = ordinaryExportFunc(func(ctx context.Context, req *reporting.RenderRequest) (*reporting.RenderResult, error) {
					result, err := reporting.NewForgeCSVExporter().Export(ctx, req)
					mu.Lock()
					switch scenario {
					case "revoked after rendering":
						allowed = false
					case "expired after rendering":
						expiry = time.Now().Add(-time.Second)
					case "drift after rendering":
						content = append(content, ' ')
					}
					mu.Unlock()
					return result, err
				})
			}
			actualExporter := exporter
			exporter = ordinaryExportFunc(func(workerCtx context.Context, req *reporting.RenderRequest) (*reporting.RenderResult, error) {
				if iauth.CanonicalUserID(workerCtx) != "workspace-ordinary-owner" || iauth.EffectiveUserID(workerCtx) != "ordinary-owner" || iauth.Provider(workerCtx) != "ordinary-workspace-session" {
					t.Error("worker lost the host-authenticated canonical user, subject or provider")
					return nil, identity.ErrResourceDenied
				}
				return actualExporter.Export(workerCtx, req)
			})
			service := reporting.New(reporting.Options{Store: store, Compiler: reporting.NewReportSpecCompiler(time.Now), Exporter: exporter, ResourceResolver: func(context.Context, string) (*identity.ResourceResolver, error) { return resolver, nil }})
			service.SetResourceDatasetExecutor(reporting.NewResourceDatasetExecutor(executor, service.AuthorizeResourceDataset))
			fixture.mu.Lock()
			if scenario == "managing MCP denied page two" {
				fixture.denyPage = 2
			}
			fixture.during = func(page int) {
				if page != 1 {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				switch scenario {
				case "revoked":
					allowed = false
				case "drift":
					content = append(content, ' ')
				case "expired":
					expiry = time.Now().Add(-time.Second)
				case "cancelled":
					cancel()
				}
			}
			fixture.mu.Unlock()
			job, err := service.SubmitExport(ctx, &reporting.SubmitExportRequest{Resource: &identity.ResourceRef{URI: uri.String()}, Format: format})
			success := scenario == "cursor" || scenario == "offset" || scenario == "page" || scenario == "unpaged" || strings.HasSuffix(scenario, " after rendering") || strings.HasSuffix(scenario, " after submit")
			if !success {
				if err == nil || job != nil {
					t.Fatalf("partial export submission escaped: %#v %v", job, err)
				}
				if scenario == "managing MCP denied page two" {
					fixture.mu.Lock()
					if len(fixture.calls) != 2 || fixture.deniedCalls != 1 {
						t.Fatalf("managing calls=%d denied=%d; expected exactly two calls/one denied without replay", len(fixture.calls), fixture.deniedCalls)
					}
					fixture.mu.Unlock()
				}
				jobs, _ := store.ListJobs(context.Background())
				artifacts, _ := store.ListArtifacts(context.Background())
				if len(jobs) != 0 || len(artifacts) != 0 {
					t.Fatal("failed fetch persisted a job or artifact")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if job.OwnerID != "ordinary-owner" || !strings.Contains(job.AuthContextRef, "canonical=workspace-ordinary-owner") || !strings.Contains(job.AuthContextRef, "provider=ordinary-workspace-session") {
				t.Fatal("pinned export did not persist the authenticated worker identity")
			}
			if scenario == "cursor" || scenario == "unpaged" {
				if !strings.Contains(string(job.ReportFill), "beta") || !strings.Contains(string(job.ReportPrint), "beta") {
					t.Fatal("complete semantic data missing from fill/print")
				}
			}
			// A caller-visible pin and a server-produced fill are insufficient to
			// select the worker's private datasource request decoder.
			if _, err := reporting.NewForgeCSVExporter().Export(ctx, &reporting.RenderRequest{Format: reporting.ExportFormatCSV, ResourcePin: job.ResourcePin, ReportFill: job.ReportFill}); err == nil {
				t.Fatal("caller pin enabled generic request decoder")
			}
			if forged, err := service.SubmitExport(ctx, &reporting.SubmitExportRequest{Resource: &identity.ResourceRef{URI: uri.String()}, ResolvedResource: job.ResourcePin, Format: format, ReportFill: job.ReportFill}); err == nil || forged != nil {
				t.Fatal("client snapshot bypassed canonical execution")
			}
			if strings.HasSuffix(scenario, " after submit") {
				var raw map[string]any
				if err := json.Unmarshal(job.ReportFill, &raw); err != nil {
					t.Fatal(err)
				}
				dataset := raw["datasets"].([]any)[0].(map[string]any)
				if scenario == "tampered request after submit" {
					dataset["request"].(map[string]any)["term"] = "changed"
				} else {
					dataset["provenance"].(map[string]any)["rowCount"] = 99
				}
				job.ReportFill, err = json.Marshal(raw)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.UpdateJob(ctx, job); err != nil {
					t.Fatal(err)
				}
			}
			completed, err := service.RunExport(context.Background(), job.JobID)
			if strings.HasSuffix(scenario, " after rendering") || strings.HasSuffix(scenario, " after submit") {
				artifacts, _ := store.ListArtifacts(context.Background())
				if err == nil || len(artifacts) != 0 {
					t.Fatal("rendered bytes escaped failed original resource guard")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			artifact, err := service.GetArtifact(ctx, completed.ArtifactID)
			if err != nil {
				t.Fatal(err)
			}
			var exported [][]string
			if format == reporting.ExportFormatPDF {
				reader, err := pdfreader.NewReader(bytes.NewReader(artifact.Data), int64(len(artifact.Data)))
				if err != nil {
					t.Fatal(err)
				}
				stream, err := reader.GetPlainText()
				if err != nil {
					t.Fatal(err)
				}
				text, err := io.ReadAll(stream)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(text), "alpha") || !strings.Contains(string(text), "beta") {
					t.Fatal("PDF lost semantic rows")
				}
				exported = [][]string{{"ID"}, {"alpha"}, {"beta"}}
			} else if format == reporting.ExportFormatXLSX {
				workbook, err := excelize.OpenReader(bytes.NewReader(artifact.Data))
				if err != nil {
					t.Fatal(err)
				}
				defer workbook.Close()
				exported, err = workbook.GetRows(workbook.GetSheetList()[0], excelize.Options{RawCellValue: true})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				exported, err = csv.NewReader(bytes.NewReader(artifact.Data)).ReadAll()
				if err != nil {
					t.Fatal(err)
				}
			}
			expectedCalls := 2
			expectedRows := [][]string{{"ID"}, {"alpha"}, {"beta"}}
			if scenario == "offset" || scenario == "page" {
				expectedCalls = 3
				// XLSX is read with RawCellValue: number formatting changes
				// presentation, not the stored integer values checked here.
				expectedRows = [][]string{{"ID"}, {"0"}, {"1"}, {"2"}, {"3"}, {"4"}, {"5"}}
			}
			if scenario == "unpaged" {
				expectedCalls = 1
			}
			if !reflect.DeepEqual(exported, expectedRows) {
				t.Fatalf("exported semantic data differs: %#v", exported)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.calls) != expectedCalls {
				t.Fatalf("export refetched or lost pages: %d", len(fixture.calls))
			}
			for _, call := range fixture.calls {
				if call.Name != method {
					t.Fatal("wrong producer tool")
				}
			}
			frozen, _ := json.Marshal(envelope)
			if string(frozen) != string(content) {
				t.Fatal("report source changed during export")
			}
		})
	}
}

type ordinaryExportFunc func(context.Context, *reporting.RenderRequest) (*reporting.RenderResult, error)

func (f ordinaryExportFunc) Export(ctx context.Context, req *reporting.RenderRequest) (*reporting.RenderResult, error) {
	return f(ctx, req)
}
