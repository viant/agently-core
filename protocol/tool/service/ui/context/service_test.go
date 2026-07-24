package context

import (
	"context"
	"reflect"
	"testing"
	"time"

	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

func TestGetProjectsConversationEventsWithoutLiveWindowSnapshot(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	svc := New(bridge)
	svc.reg.RecordConversationEvent("conv-1", uireg.UIEvent{
		WindowID:  "reportBuilder__conv-1",
		WindowKey: "reportBuilder",
		Kind:      "report.context_updated",
		Detail: map[string]interface{}{
			"reportName": "Performance Brief",
			"reportId":   "performance",
			"sourceKind": "preset",
			"filters":    map[string]interface{}{"orderId": 2680567},
		},
	})
	svc.reg.RecordConversationEvent("conv-1", uireg.UIEvent{
		WindowID:  "reportBuilder__conv-1",
		WindowKey: "reportBuilder",
		Kind:      "report.run",
		Detail: map[string]interface{}{
			"reportName": "Performance Brief",
			"reportId":   "performance",
			"sourceKind": "preset",
			"filters":    map[string]interface{}{"orderId": 2680567},
			"runId":      "run-1",
			"status":     "succeeded",
		},
	})

	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	out := &GetOutput{}
	if err := svc.get(ctx, &GetInput{WindowKey: "reportBuilder"}, out); err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if len(out.Windows) != 0 || len(out.RecentEvents) != 2 {
		t.Fatalf("expected event context without a live window snapshot, got %#v", out)
	}
	if len(out.CurrentReports) != 1 || out.CurrentReports[0].LatestRun == nil {
		t.Fatalf("expected current report projection from conversation events, got %#v", out.CurrentReports)
	}
}

func TestBuildCurrentReportContextsCorrelatesLatestRunAndExport(t *testing.T) {
	events := []uireg.UIEvent{
		{Seq: 1, WindowID: "window-1", WindowKey: "performance", Kind: "report.context_updated", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 2680567},
		}},
		{Seq: 2, WindowID: "window-1", WindowKey: "performance", Kind: "report.run", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 2680567}, "runId": "run-1",
		}},
		{Seq: 3, WindowID: "window-1", WindowKey: "performance", Kind: "report.export_complete", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 2680567}, "artifactId": "artifact-1",
		}},
	}
	contexts := buildCurrentReportContexts(events)
	if len(contexts) != 1 {
		t.Fatalf("expected one report context, got %#v", contexts)
	}
	current := contexts[0]
	if current.ReportName != "Performance Brief" || current.LatestRun == nil || current.LatestRun.Seq != 2 || current.LatestExport == nil || current.LatestExport.Seq != 3 {
		t.Fatalf("unexpected current report context: %#v", current)
	}
	if current.Filters["orderId"] != float64(2680567) && current.Filters["orderId"] != 2680567 {
		t.Fatalf("expected order filter, got %#v", current.Filters)
	}
}

func TestBuildCurrentReportContextsUsesCompletedEventsMatchingActiveScope(t *testing.T) {
	events := []uireg.UIEvent{
		{Seq: 8, WindowID: "reportBuilder__conv-1", WindowKey: "reportBuilder", Kind: "report.export_start", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 2680567},
		}},
		{Seq: 4, WindowID: "reportBuilder__conv-1", WindowKey: "reportBuilder", Kind: "report.run", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 2680567}, "runId": "run-1", "status": "succeeded",
		}},
		{Seq: 7, WindowID: "reportBuilder__conv-1", WindowKey: "reportBuilder", Kind: "report.run_start", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 2680567}, "runId": "run-2",
		}},
		{Seq: 6, WindowID: "reportBuilder__conv-1", WindowKey: "reportBuilder", Kind: "report.export_complete", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "filters": map[string]interface{}{"orderId": 999}, "artifactId": "wrong-scope",
		}},
		{Seq: 5, WindowID: "reportBuilder__conv-1", WindowKey: "reportBuilder", Kind: "report.export_complete", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "artifactRef": "scratchpad://artifact/artifact-1", "filters": map[string]interface{}{"orderId": 2680567}, "artifactId": "artifact-1", "status": "succeeded",
		}},
		{Seq: 9, WindowID: "reportBuilder__conv-1", WindowKey: "reportBuilder", Kind: "report.context_updated", Detail: map[string]interface{}{
			"reportName": "Performance Brief", "reportId": "performance", "artifactRef": "report://performance", "sourceKind": "preset", "filters": map[string]interface{}{"orderId": 2680567},
		}},
	}

	contexts := buildCurrentReportContexts(events)
	if len(contexts) != 1 {
		t.Fatalf("expected one current report, got %#v", contexts)
	}
	current := contexts[0]
	if current.LatestRun == nil || current.LatestRun.Seq != 4 {
		t.Fatalf("expected latest completed matching run, got %#v", current.LatestRun)
	}
	if current.LatestExport == nil || current.LatestExport.Seq != 5 {
		t.Fatalf("expected latest completed exact-scope export, got %#v", current.LatestExport)
	}
	if current.ContextEvent == nil || current.ContextEvent.Seq != 9 || current.SourceKind != "preset" {
		t.Fatalf("expected newest context anchor, got %#v", current)
	}
}

func TestBuildCurrentReportContextsDoesNotReuseExportWithoutArtifact(t *testing.T) {
	events := []uireg.UIEvent{
		{Seq: 1, WindowID: "window-1", Kind: "report.context_updated", Detail: map[string]interface{}{
			"reportName": "Inline Brief", "filters": map[string]interface{}{"orderId": 7},
		}},
		{Seq: 2, WindowID: "window-1", Kind: "report.export_complete", Detail: map[string]interface{}{
			"reportName": "Inline Brief", "filters": map[string]interface{}{"orderId": 7}, "status": "succeeded",
		}},
	}
	contexts := buildCurrentReportContexts(events)
	if len(contexts) != 1 || contexts[0].LatestExport != nil {
		t.Fatalf("export without artifactId must not be reusable: %#v", contexts)
	}
}

func TestNewestFirstUIEventsUsesSequenceThenTimestamp(t *testing.T) {
	base := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	events := []uireg.UIEvent{
		{Seq: 1, At: base, Kind: "first"},
		{Seq: 3, At: base, Kind: "third"},
		{Seq: 2, At: base.Add(time.Minute), Kind: "second"},
	}
	ordered := newestFirstUIEvents(events)
	got := []string{ordered[0].Kind, ordered[1].Kind, ordered[2].Kind}
	if !reflect.DeepEqual(got, []string{"third", "second", "first"}) {
		t.Fatalf("expected newest-first events, got %#v", got)
	}
}
