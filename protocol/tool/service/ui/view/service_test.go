package view

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/viant/afs"
	windowtool "github.com/viant/agently-core/protocol/tool/service/ui/window"
	viewproto "github.com/viant/agently-core/protocol/ui/view"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	"github.com/viant/agently-core/workspace"
	repo "github.com/viant/agently-core/workspace/repository/forgewindow"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

func TestExpandOpenParametersBindsOneInputToMultipleTargets(t *testing.T) {
	specParams := []viewproto.Parameter{
		{Name: "RecordId", BindTo: "order_performance_profile.parameters.RecordId"},
		{Name: "RecordId", BindTo: "order_performance_period_today.parameters.RecordId"},
		{Name: "RecordId", BindTo: "order_performance_period_yesterday.parameters.RecordId"},
		{Name: "RecordId", BindTo: "order_performance_period_7d.parameters.RecordId"},
		{Name: "RecordId", BindTo: "order_performance_period_30d.parameters.RecordId"},
	}

	actual := expandOpenParameters(specParams, map[string]interface{}{
		"RecordId": []interface{}{2664124.0},
	})

	assertNestedValue(t, actual, []interface{}{2664124.0}, "order_performance_profile", "parameters", "RecordId")
	assertNestedValue(t, actual, []interface{}{2664124.0}, "order_performance_period_today", "parameters", "RecordId")
	assertNestedValue(t, actual, []interface{}{2664124.0}, "order_performance_period_yesterday", "parameters", "RecordId")
	assertNestedValue(t, actual, []interface{}{2664124.0}, "order_performance_period_7d", "parameters", "RecordId")
	assertNestedValue(t, actual, []interface{}{2664124.0}, "order_performance_period_30d", "parameters", "RecordId")
}

func TestExpandOpenParametersPreservesUnboundParameters(t *testing.T) {
	specParams := []viewproto.Parameter{
		{Name: "RecordId", BindTo: "order_performance_profile.parameters.RecordId"},
	}

	actual := expandOpenParameters(specParams, map[string]interface{}{
		"RecordId": []interface{}{2664124.0},
		"ClientID": "client-1",
	})

	if actual["ClientID"] != "client-1" {
		t.Fatalf("expected passthrough ClientID, got %#v", actual["ClientID"])
	}
}

func TestExpandOpenParametersBindsSemanticBuilderPrefill(t *testing.T) {
	specParams := []viewproto.Parameter{
		{Name: "customerId", BindTo: "prefill.customerId"},
		{Name: "dealId", BindTo: "prefill.dealId"},
		{Name: "targetingIncl", BindTo: "prefill.targetingIncl"},
	}

	actual := expandOpenParameters(specParams, map[string]interface{}{
		"customerId":    123.0,
		"dealId":        778899.0,
		"targetingIncl": "iris:1466062,123",
	})

	assertNestedValue(t, actual, 123.0, "prefill", "customerId")
	assertNestedValue(t, actual, 778899.0, "prefill", "dealId")
	assertNestedValue(t, actual, "iris:1466062,123", "prefill", "targetingIncl")
}

func TestMissingRequiredParameters(t *testing.T) {
	specParams := []viewproto.Parameter{
		{Name: "RecordId", Required: true, BindTo: "order_performance_profile.parameters.RecordId"},
		{Name: "RecordId", Required: true, BindTo: "order_performance_period_today.parameters.RecordId"},
	}

	if missing := missingRequiredParameters(specParams, nil); len(missing) != 1 || missing[0] != "RecordId" {
		t.Fatalf("expected RecordId to be reported missing, got %#v", missing)
	}

	if missing := missingRequiredParameters(specParams, map[string]interface{}{
		"RecordId": []interface{}{2664124.0},
	}); len(missing) != 0 {
		t.Fatalf("expected no missing required parameters, got %#v", missing)
	}
}

func TestAvailableViewIDs(t *testing.T) {
	items := []ListItem{
		{ID: "orderPerformance"},
		{ID: " approvals "},
		{ID: ""},
	}
	got := availableViewIDs(items)
	if len(got) != 2 {
		t.Fatalf("expected 2 ids, got %#v", got)
	}
	if got[0] != "approvals" || got[1] != "orderPerformance" {
		t.Fatalf("unexpected ids: %#v", got)
	}
}

func TestBuildOpenWindowOptionsCarriesNavigationMetadata(t *testing.T) {
	options := buildOpenWindowOptions(&ListItem{
		Presentation: "hosted",
		Region:       "chat.top",
		Navigation:   &viewproto.Navigation{Label: "Reports", Icon: "chart"},
	}, "conv-1", "")
	navigation, ok := options["navigation"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected navigation options, got %#v", options["navigation"])
	}
	if navigation["label"] != "Reports" || navigation["icon"] != "chart" {
		t.Fatalf("unexpected navigation metadata: %#v", navigation)
	}
}

func TestResolveReportStarterIDCanonicalization(t *testing.T) {
	presets := []viewproto.ReportPreset{
		{ID: "Alpha_ID", Label: "Alpha Report"},
		{ID: "beta_id", Label: "Beta Report"},
	}
	tests := []struct {
		name       string
		parameters map[string]interface{}
		wantValue  interface{}
		wantExists bool
		want       *viewproto.ReportPresetResolution
	}{
		{
			name:       "exact canonical id",
			parameters: map[string]interface{}{"reportStarterId": "  Alpha_ID  "},
			wantValue:  "Alpha_ID",
			wantExists: true,
			want:       &viewproto.ReportPresetResolution{Requested: "Alpha_ID", ResolvedID: "Alpha_ID", MatchedBy: "id"},
		},
		{
			name:       "case insensitive canonical id",
			parameters: map[string]interface{}{"reportStarterId": "alpha_id"},
			wantValue:  "Alpha_ID",
			wantExists: true,
			want:       &viewproto.ReportPresetResolution{Requested: "alpha_id", ResolvedID: "Alpha_ID", MatchedBy: "id"},
		},
		{
			name:       "normalized label",
			parameters: map[string]interface{}{"reportStarterId": "  aLPHa \t REPORT  "},
			wantValue:  "Alpha_ID",
			wantExists: true,
			want:       &viewproto.ReportPresetResolution{Requested: "aLPHa \t REPORT", ResolvedID: "Alpha_ID", MatchedBy: "label"},
		},
		{
			name:       "omitted",
			parameters: map[string]interface{}{"executeOnOpen": true},
		},
		{
			name:       "whitespace behaves as omitted",
			parameters: map[string]interface{}{"reportStarterId": " \t\n "},
		},
		{
			name:       "forge blank starter is reserved",
			parameters: map[string]interface{}{"reportStarterId": "  __blank__  "},
			wantValue:  "__blank__",
			wantExists: true,
			want:       &viewproto.ReportPresetResolution{Requested: "__blank__", ResolvedID: "__blank__", MatchedBy: "reserved"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolution, err := resolveReportStarterID(test.parameters, presets)
			if err != nil {
				t.Fatalf("resolve report starter: %v", err)
			}
			if !reflect.DeepEqual(resolution, test.want) {
				t.Fatalf("unexpected resolution: got=%#v want=%#v", resolution, test.want)
			}
			value, exists := test.parameters["reportStarterId"]
			if exists != test.wantExists || (exists && value != test.wantValue) {
				t.Fatalf("unexpected canonical parameter: exists=%v value=%#v", exists, value)
			}
		})
	}
}

func TestResolveReportStarterIDRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name      string
		requested interface{}
		presets   []viewproto.ReportPreset
		wantError string
	}{
		{
			name:      "unknown lists stable sorted nonblank labels",
			requested: "Alpha-Report",
			presets: []viewproto.ReportPreset{
				{ID: "secret_zeta_id", Label: " Zeta "},
				{ID: "secret_blank_id", Label: " \t "},
				{ID: "secret_alpha_id", Label: "  Alpha   Report  "},
			},
			wantError: `unknown reportStarterId "Alpha-Report"; available preset labels: "Alpha Report", "Zeta"; retry with a listed label or inspect ui/view:get`,
		},
		{
			name:      "ambiguous normalized label",
			requested: " shared  label ",
			presets: []viewproto.ReportPreset{
				{ID: "secret_second_id", Label: "SHARED LABEL"},
				{ID: "secret_first_id", Label: "Shared   Label"},
			},
			wantError: `reportStarterId label "shared  label" is ambiguous; inspect ui/view:get for the preset catalog and use an unambiguous canonical ID`,
		},
		{
			name:      "non string",
			requested: 42,
			presets: []viewproto.ReportPreset{
				{ID: "secret_beta_id", Label: "beta Report"},
				{ID: "secret_alpha_id", Label: "Alpha Report"},
			},
			wantError: `reportStarterId must be a string; available preset labels: "Alpha Report", "beta Report"; retry with a listed label or inspect ui/view:get`,
		},
		{
			name:      "case insensitive id collision",
			requested: "CASEID",
			presets: []viewproto.ReportPreset{
				{ID: "CaseID", Label: "First"},
				{ID: "caseid", Label: "Second"},
			},
			wantError: "report preset catalog is invalid/ambiguous: canonical IDs collide case-insensitively; inspect ui/view:get before retrying",
		},
		{
			name:      "empty catalog",
			requested: "Missing Report",
			wantError: `unknown reportStarterId "Missing Report"; no usable report preset labels are available; inspect ui/view:get before retrying`,
		},
		{
			name:      "catalog with only blank labels",
			requested: "Missing Report",
			presets: []viewproto.ReportPreset{
				{ID: "secret_empty_id", Label: ""},
				{ID: "secret_whitespace_id", Label: " \t\n "},
			},
			wantError: `unknown reportStarterId "Missing Report"; no usable report preset labels are available; inspect ui/view:get before retrying`,
		},
		{
			name:      "ambiguous only catalog has no usable labels",
			requested: "Missing Report",
			presets: []viewproto.ReportPreset{
				{ID: "secret_first_id", Label: " Shared   Label "},
				{ID: "secret_second_id", Label: "shared label"},
			},
			wantError: `unknown reportStarterId "Missing Report"; no usable report preset labels are available; inspect ui/view:get before retrying`,
		},
		{
			name:      "mixed catalog lists only unique labels in stable order",
			requested: 42,
			presets: []viewproto.ReportPreset{
				{ID: "secret_zeta_id", Label: " zeta Report "},
				{ID: "secret_beta_first_id", Label: "Beta Report"},
				{ID: "secret_alpha_id", Label: "  Alpha   Report  "},
				{ID: "secret_beta_second_id", Label: " beta   report "},
				{ID: "", Label: "Before Alpha"},
			},
			wantError: `reportStarterId must be a string; available preset labels: "Alpha Report", "zeta Report"; retry with a listed label or inspect ui/view:get`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parameters := map[string]interface{}{
				"reportStarterId": test.requested,
				"options":         map[string]interface{}{"theme": "dark"},
			}
			snapshot := cloneMap(parameters)
			resolution, err := resolveReportStarterID(parameters, test.presets)
			if err == nil {
				t.Fatalf("expected report starter resolution error")
			}
			if resolution != nil {
				t.Fatalf("unexpected resolution on error: %#v", resolution)
			}
			if err.Error() != test.wantError {
				t.Fatalf("unexpected error: got=%q want=%q", err, test.wantError)
			}
			for _, preset := range test.presets {
				if id := strings.TrimSpace(preset.ID); id != "" && strings.Contains(err.Error(), id) {
					t.Fatalf("resolver error exposed canonical preset ID %q: %v", id, err)
				}
			}
			if !reflect.DeepEqual(parameters, snapshot) {
				t.Fatalf("resolution failure mutated parameters: got=%#v want=%#v", parameters, snapshot)
			}
		})
	}
}

func TestPrepareOpenItemCanonicalizesRawStarterBeforeBinding(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "report.yaml"), `
id: report
title: Report
windowKey: reportBuilder
reportBuilderRef: performance
parameters:
  - name: reportStarterId
    bindTo: prefill.reportStarterId
reportPresets:
  - id: Alpha_ID
    label: Alpha Report
`)
		parameters := map[string]interface{}{
			"reportStarterId": "  alpha   REPORT ",
			"executeOnOpen":   true,
		}
		snapshot := cloneMap(parameters)
		viewSvc := New(repo.New(afs.New()), nil)
		prepared, err := viewSvc.prepareOpenItem(context.Background(), OpenItem{
			ID:         "report",
			Parameters: parameters,
		})
		if err != nil {
			t.Fatalf("prepare open item: %v", err)
		}
		if prepared.reportPresetResolution == nil ||
			prepared.reportPresetResolution.Requested != "alpha   REPORT" ||
			prepared.reportPresetResolution.ResolvedID != "Alpha_ID" ||
			prepared.reportPresetResolution.MatchedBy != "label" {
			t.Fatalf("unexpected report preset resolution: %#v", prepared.reportPresetResolution)
		}
		if prepared.windowParameters["reportStarterId"] != "Alpha_ID" {
			t.Fatalf("canonical starter did not reach top-level Forge parameters: %#v", prepared.windowParameters)
		}
		assertNestedValue(t, prepared.windowParameters, "Alpha_ID", "prefill", "reportStarterId")
		if prepared.windowParameters["reportBuilderRef"] != "performance" {
			t.Fatalf("report builder reference was not preserved: %#v", prepared.windowParameters)
		}
		if !reflect.DeepEqual(parameters, snapshot) {
			t.Fatalf("preparation mutated caller parameters: got=%#v want=%#v", parameters, snapshot)
		}
	})
}

func TestLoadOneReturnsTypedNotFoundWithAvailableIDs(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "analytics.yaml"), `
id: analytics
title: Analytics
windowKey: analytics
`)
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "orders.yaml"), `
id: orders
title: Orders
windowKey: orders
`)
		svc := &Service{repo: repo.New(afs.New())}
		_, err := svc.loadOne(context.Background(), "legacyAlias")
		var notFound *viewNotFoundError
		if !errors.As(err, &notFound) {
			t.Fatalf("expected viewNotFoundError, got %T %v", err, err)
		}
		if notFound.id != "legacyAlias" {
			t.Fatalf("unexpected missing id: %q", notFound.id)
		}
		if got := strings.Join(notFound.available, ","); got != "analytics,orders" {
			t.Fatalf("unexpected available ids: %#v", notFound.available)
		}
	})
}

func TestOpenResolvedItemRecordsInvalidWorkspaceIDEvent(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "orders.yaml"), `
id: orders
title: Orders
windowKey: orders
presentation: hosted
region: chat.top
`)
		const conversationID = "conv-invalid-view"
		const clientID = "mobile-client-1"

		bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
		viewSvc := New(repo.New(afs.New()), bridge)
		_, err := viewSvc.openResolvedItem(
			context.Background(),
			clientID,
			"",
			conversationID,
			OpenItem{ID: "legacyAlias"},
			1,
		)
		var notFound *viewNotFoundError
		if !errors.As(err, &notFound) {
			t.Fatalf("expected viewNotFoundError, got %T %v", err, err)
		}

		events := viewSvc.reg.ListEvents(conversationID, clientID, "", "", 10, 0)
		if len(events) != 1 {
			t.Fatalf("expected one event, got %#v", events)
		}
		event := events[0]
		if event.Kind != "error" {
			t.Fatalf("expected error event, got %#v", event)
		}
		payload, ok := event.Detail["payload"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected payload map, got %#v", event.Detail)
		}
		if payload["invalidWorkspaceId"] != "legacyAlias" {
			t.Fatalf("expected invalidWorkspaceId, got %#v", payload)
		}
		available, ok := payload["availableWorkspaceIds"].([]string)
		if !ok {
			t.Fatalf("expected availableWorkspaceIds, got %#v", payload["availableWorkspaceIds"])
		}
		if got := strings.Join(available, ","); got != "orders" {
			t.Fatalf("unexpected available ids: %#v", available)
		}
	})
}

func TestBuildOpenWindowOptions_HostedViewsAttachToChatRootAndReplaceRegion(t *testing.T) {
	item := &ListItem{
		WindowKey:    "order",
		Presentation: "hosted",
		Region:       "chat.top",
		OpenMode:     "replace",
	}
	got := buildOpenWindowOptions(item, "conv-1", "")
	if got["conversationId"] != "conv-1" {
		t.Fatalf("expected conversationId, got %#v", got["conversationId"])
	}
	if got["parentKey"] != "chat/new" {
		t.Fatalf("expected chat/new parentKey, got %#v", got["parentKey"])
	}
	if got["replaceHostedRegion"] != true {
		t.Fatalf("expected replaceHostedRegion=true, got %#v", got["replaceHostedRegion"])
	}
}

func TestBuildOpenWindowOptions_NonHostedViewsDoNotForceHostedOwnership(t *testing.T) {
	item := &ListItem{
		WindowKey:    "schedule",
		Presentation: "",
		Region:       "",
	}
	got := buildOpenWindowOptions(item, "conv-1", "")
	if _, ok := got["parentKey"]; ok {
		t.Fatalf("did not expect parentKey for non-hosted view")
	}
	if _, ok := got["replaceHostedRegion"]; ok {
		t.Fatalf("did not expect replaceHostedRegion for non-hosted view")
	}
}

func TestBuildOpenWindowOptions_AppendOverrideDisablesReplacement(t *testing.T) {
	item := &ListItem{
		WindowKey:    "order",
		Presentation: "hosted",
		Region:       "chat.top",
		OpenMode:     "replace",
	}
	got := buildOpenWindowOptions(item, "conv-1", "append")
	if got["replaceHostedRegion"] != false {
		t.Fatalf("expected replaceHostedRegion=false for append override, got %#v", got["replaceHostedRegion"])
	}
}

func TestBuildOpenWindowOptions_PreservesWorkspaceLayoutHints(t *testing.T) {
	item := &ListItem{
		WindowKey:          "order",
		Presentation:       "hosted",
		Region:             "chat.top",
		WorkspaceSharePct:  72,
		WorkspaceMinHeight: 500,
	}
	got := buildOpenWindowOptions(item, "conv-1", "")
	if got["workspaceSharePct"] != 72 {
		t.Fatalf("expected workspaceSharePct=72, got %#v", got["workspaceSharePct"])
	}
	if got["workspaceMinHeight"] != 500 {
		t.Fatalf("expected workspaceMinHeight=500, got %#v", got["workspaceMinHeight"])
	}
}

func TestServiceLoadAll_PreservesWorkspaceLayoutHintsFromSpec(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "order.yaml"), `
id: order
title: Order Summary
windowKey: order
presentation: hosted
region: chat.top
workspaceSharePct: 72
workspaceMinHeight: 500
`)
		svc := &Service{
			repo: repo.New(afs.New()),
		}
		items, err := svc.loadAll(context.Background())
		if err != nil {
			t.Fatalf("loadAll failed: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("expected one view item, got %#v", items)
		}
		if items[0].WorkspaceSharePct != 72 {
			t.Fatalf("expected workspaceSharePct=72, got %#v", items[0].WorkspaceSharePct)
		}
		if items[0].WorkspaceMinHeight != 500 {
			t.Fatalf("expected workspaceMinHeight=500, got %#v", items[0].WorkspaceMinHeight)
		}
	})
}

func TestServiceLoadAll_PreservesReportPresetCatalog(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "report.yaml"), `
id: report
title: Report
windowKey: report
reportPresets:
  - id: inventory_brief
    label: Inventory Brief
    description: Channel-first inventory report.
`)
		svc := &Service{repo: repo.New(afs.New())}
		items, err := svc.loadAll(context.Background())
		if err != nil {
			t.Fatalf("loadAll failed: %v", err)
		}
		if len(items) != 1 || len(items[0].ReportPresets) != 1 {
			t.Fatalf("expected one report preset, got %#v", items)
		}
		preset := items[0].ReportPresets[0]
		if preset.ID != "inventory_brief" || preset.Label != "Inventory Brief" || preset.Description == "" {
			t.Fatalf("unexpected report preset: %#v", preset)
		}
	})
}

func TestComputeWindowIDConversationIdentityIgnoresReportSourceParameters(t *testing.T) {
	item := &ListItem{
		WindowKey:     "reportBuilder",
		Presentation:  "hosted",
		IdentityScope: "conversation",
	}
	primary := computeWindowID("reportBuilder", map[string]interface{}{
		"reportBuilderRef": "primary",
		"orderIds":         []interface{}{2680567},
	}, "conv-1", item)
	secondary := computeWindowID("reportBuilder", map[string]interface{}{
		"reportBuilderRef": "secondary",
		"audienceIds":      []interface{}{7113447},
	}, "conv-1", item)
	if primary != "reportBuilder__conv-1" || secondary != primary {
		t.Fatalf("expected one conversation-scoped report window, got primary=%q secondary=%q", primary, secondary)
	}
}

func TestComputeWindowIDUsesViewIDForAliasedHostedViewIdentity(t *testing.T) {
	item := &ListItem{
		ID:            "forecastingCubeBuilder",
		WindowKey:     "reportBuilder",
		Presentation:  "hosted",
		IdentityScope: "conversation",
	}
	got := computeWindowID(item.WindowKey, map[string]interface{}{
		"reportBuilderRef": "forecastingCubeBuilder",
	}, "conv-forecast", item)
	if got != "forecastingCubeBuilder__conv-forecast" {
		t.Fatalf("unexpected aliased hosted window id: %s", got)
	}
}

func TestServiceLoadAll_EnrichesReportPresetsFromBuilderReference(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "report.yaml"), `
id: report
title: Report
windowKey: report
reportBuilderRef: performance
`)
		svc := New(repo.New(afs.New()), nil, WithListItemEnricher(func(_ context.Context, item *ListItem) error {
			if item.ReportBuilderRef == "performance" {
				item.ReportPresets = []viewproto.ReportPreset{{ID: "command_center", Label: "Command Center"}}
			}
			return nil
		}))
		items, err := svc.loadAll(context.Background())
		if err != nil {
			t.Fatalf("loadAll failed: %v", err)
		}
		if len(items) != 1 || items[0].ReportBuilderRef != "performance" || len(items[0].ReportPresets) != 1 {
			t.Fatalf("expected registry-enriched report view, got %#v", items)
		}
	})
}

func TestServiceLoadAll_LoadsImportOnlyForgeViewSpecs(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "order.yaml"), `
$import(order/shared/main.yaml)
`)
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "order", "shared", "main.yaml"), `
$import(web/main.yaml)
`)
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "order", "shared", "web", "main.yaml"), `
id: order
title: Order Summary
windowKey: order
presentation: hosted
region: chat.top
workspaceSharePct: 72
workspaceMinHeight: 500
`)
		svc := &Service{
			repo: repo.New(afs.New()),
		}
		items, err := svc.loadAll(context.Background())
		if err != nil {
			t.Fatalf("loadAll failed: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("expected one view item, got %#v", items)
		}
		if items[0].ID != "order" || items[0].WindowKey != "order" {
			t.Fatalf("unexpected item: %#v", items[0])
		}
	})
}

func TestOpenCanonicalizesReportStarterAcrossCommandOutputAndEvent(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "report.yaml"), `
id: report
title: Report
windowKey: reportBuilder
presentation: hosted
reportBuilderRef: performance
parameters:
  - name: reportStarterId
    bindTo: prefill.reportStarterId
reportPresets:
  - id: Alpha_ID
    label: Alpha Report
`)
		const conversationID = "conv-report-preset"
		const clientID = "client-report-preset"
		bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
		postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
			"clientId": clientID,
			"data": map[string]interface{}{
				"clientId":       clientID,
				"conversationId": conversationID,
				"selected":       map[string]interface{}{"windowId": "chat/new"},
				"windows": []interface{}{map[string]interface{}{
					"windowId":       "chat/new",
					"windowKey":      "chat/new",
					"conversationId": conversationID,
				}},
			},
		})
		waitForSnapshotEntry(t, bridge, clientID)

		commandDone := make(chan error, 1)
		go func() {
			result := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": clientID, "timeoutMs": 2_000})
			request, ok := result["params"].(map[string]interface{})
			if !ok || request["method"] != "ui.window.open" {
				commandDone <- fmt.Errorf("unexpected bridge command: %#v", result)
				return
			}
			params, _ := request["params"].(map[string]interface{})
			windowParameters, _ := params["parameters"].(map[string]interface{})
			if windowParameters["reportStarterId"] != "Alpha_ID" {
				commandDone <- fmt.Errorf("missing canonical top-level starter: %#v", windowParameters)
				return
			}
			prefill, _ := windowParameters["prefill"].(map[string]interface{})
			if prefill["reportStarterId"] != "Alpha_ID" ||
				windowParameters["reportBuilderRef"] != "performance" ||
				windowParameters["executeOnOpen"] != true {
				commandDone <- fmt.Errorf("unexpected canonical command parameters: %#v", windowParameters)
				return
			}
			postUIRPC(t, bridge, "ui.response", map[string]interface{}{
				"id":     request["id"],
				"ok":     true,
				"result": map[string]interface{}{"windowId": params["windowId"]},
			})
			commandDone <- nil
		}()

		parameters := map[string]interface{}{
			"reportStarterId": "  alpha   report ",
			"executeOnOpen":   true,
		}
		snapshot := cloneMap(parameters)
		ctx := runtimerequestctx.WithConversationID(context.Background(), conversationID)
		viewSvc := New(repo.New(afs.New()), bridge)
		output := &OpenOutput{}
		if err := viewSvc.open(ctx, &OpenInput{
			ID:         "report",
			Parameters: parameters,
			ClientID:   clientID,
			TimeoutMs:  2_000,
		}, output); err != nil {
			t.Fatalf("open canonical report preset: %v", err)
		}
		if err := <-commandDone; err != nil {
			t.Fatalf("bridge command handling failed: %v", err)
		}
		if !reflect.DeepEqual(parameters, snapshot) {
			t.Fatalf("open mutated caller parameters: got=%#v want=%#v", parameters, snapshot)
		}
		if output.Parameters["reportStarterId"] != "Alpha_ID" ||
			output.ReportPresetResolution == nil ||
			output.ReportPresetResolution.Requested != "alpha   report" ||
			output.ReportPresetResolution.ResolvedID != "Alpha_ID" ||
			output.ReportPresetResolution.MatchedBy != "label" {
			t.Fatalf("unexpected top-level output: %#v", output)
		}
		if len(output.Items) != 1 ||
			output.Items[0].Parameters["reportStarterId"] != "Alpha_ID" ||
			!reflect.DeepEqual(output.Items[0].ReportPresetResolution, output.ReportPresetResolution) {
			t.Fatalf("unexpected per-item output metadata: %#v", output.Items)
		}
		encoded, err := json.Marshal(output)
		if err != nil {
			t.Fatalf("marshal output: %v", err)
		}
		if !strings.Contains(string(encoded), `"reportPresetResolution":{"requested":"alpha   report","resolvedId":"Alpha_ID","matchedBy":"label"}`) {
			t.Fatalf("protocol output omitted report preset resolution: %s", encoded)
		}

		events := viewSvc.reg.ListEvents(conversationID, clientID, "", "reportBuilder", 10, 0)
		if len(events) != 1 || events[0].Kind != "view.open" {
			t.Fatalf("expected one view.open event, got %#v", events)
		}
		eventParameters, _ := events[0].Detail["parameters"].(map[string]interface{})
		if eventParameters["reportStarterId"] != "Alpha_ID" {
			t.Fatalf("event did not preserve canonical parameters: %#v", events[0].Detail)
		}
		eventResolution, ok := events[0].Detail["reportPresetResolution"].(*viewproto.ReportPresetResolution)
		if !ok || !reflect.DeepEqual(eventResolution, output.ReportPresetResolution) {
			t.Fatalf("event did not preserve resolution metadata: %#v", events[0].Detail)
		}
	})
}

func TestOpenReturnsBrowserWindowRejectionWithoutResultOrEvent(t *testing.T) {
	tests := []struct {
		name          string
		responseError string
		wantError     string
	}{
		{
			name:          "browser error",
			responseError: "window policy denied the request",
			wantError:     `ui.window.open rejected for view "report": window policy denied the request`,
		},
		{
			name:          "blank browser error",
			responseError: " \t ",
			wantError:     `ui.window.open rejected for view "report": ` + uiWindowOpenRejectionFallbackText,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withWorkspaceRoot(t, func(root string) {
				mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "report.yaml"), `
id: report
title: Report
windowKey: reportBuilder
presentation: hosted
capabilities:
  datasource: true
`)
				conversationID := "conv-window-rejection-" + strings.ReplaceAll(test.name, " ", "-")
				clientID := "client-window-rejection-" + strings.ReplaceAll(test.name, " ", "-")
				bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
				postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
					"clientId": clientID,
					"data": map[string]interface{}{
						"clientId":       clientID,
						"conversationId": conversationID,
						"windows": []interface{}{map[string]interface{}{
							"windowId":       "chat/new",
							"windowKey":      "chat/new",
							"conversationId": conversationID,
						}},
					},
				})

				commandDone := make(chan error, 1)
				go func() {
					result := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": clientID, "timeoutMs": 2_000})
					request, ok := result["params"].(map[string]interface{})
					if !ok || request["method"] != "ui.window.open" {
						commandDone <- fmt.Errorf("unexpected bridge command: %#v", result)
						return
					}
					postUIRPC(t, bridge, "ui.response", map[string]interface{}{
						"id":    request["id"],
						"ok":    false,
						"error": test.responseError,
					})
					commandDone <- nil
				}()

				ctx := runtimerequestctx.WithConversationID(context.Background(), conversationID)
				viewSvc := New(repo.New(afs.New()), bridge)
				output := &OpenOutput{}
				err := viewSvc.open(ctx, &OpenInput{
					ID:        "report",
					ClientID:  clientID,
					TimeoutMs: 2_000,
				}, output)
				if commandErr := <-commandDone; commandErr != nil {
					t.Fatalf("bridge command handling failed: %v", commandErr)
				}
				if err == nil || err.Error() != test.wantError {
					t.Fatalf("unexpected open rejection: got=%v want=%q", err, test.wantError)
				}
				if output.OK || output.Error != test.wantError {
					t.Fatalf("expected failed top-level output, got %#v", output)
				}
				if len(output.Items) != 0 {
					t.Fatalf("rejected open must not append a result item: %#v", output.Items)
				}
				if events := viewSvc.reg.ListEvents(conversationID, clientID, "", "reportBuilder", 10, 0); len(events) != 0 {
					t.Fatalf("rejected open must not record view.open event: %#v", events)
				}
				assertNoUICommand(t, bridge, clientID)
			})
		})
	}
}

func TestOpenRejectsInvalidReportStarterBeforeBridgeCommand(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "report.yaml"), `
id: report
title: Report
windowKey: reportBuilder
reportPresets:
  - id: Alpha_ID
    label: Alpha Report
`)
		const conversationID = "conv-invalid-preset"
		const clientID = "client-invalid-preset"
		bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
		postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
			"clientId": clientID,
			"data": map[string]interface{}{
				"clientId":       clientID,
				"conversationId": conversationID,
				"windows": []interface{}{map[string]interface{}{
					"windowId":  "chat/new",
					"windowKey": "chat/new",
				}},
			},
		})

		parameters := map[string]interface{}{"reportStarterId": "Alpha-Report"}
		snapshot := cloneMap(parameters)
		ctx := runtimerequestctx.WithConversationID(context.Background(), conversationID)
		viewSvc := New(repo.New(afs.New()), bridge)
		err := viewSvc.open(ctx, &OpenInput{
			ID:         "report",
			Parameters: parameters,
			ClientID:   clientID,
			TimeoutMs:  50,
		}, &OpenOutput{})
		if err == nil || !strings.Contains(err.Error(), `unknown reportStarterId "Alpha-Report"`) ||
			!strings.Contains(err.Error(), `"Alpha Report"`) ||
			strings.Contains(err.Error(), "Alpha_ID") {
			t.Fatalf("expected actionable report preset error, got %v", err)
		}
		if !reflect.DeepEqual(parameters, snapshot) {
			t.Fatalf("rejected open mutated caller parameters: got=%#v want=%#v", parameters, snapshot)
		}
		if events := viewSvc.reg.ListEvents(conversationID, clientID, "", "", 10, 0); len(events) != 0 {
			t.Fatalf("rejected open must not record an event: %#v", events)
		}
		assertNoUICommand(t, bridge, clientID)
	})
}

func TestOpenBatchValidationIsAtomicWhenLaterStarterIsInvalid(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "reportA.yaml"), `
id: reportA
title: Report A
windowKey: reportA
reportPresets:
  - id: alpha_id
    label: Alpha Report
`)
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "reportB.yaml"), `
id: reportB
title: Report B
windowKey: reportB
reportPresets:
  - id: beta_id
    label: Beta Report
`)
		const conversationID = "conv-atomic-preset"
		const clientID = "client-atomic-preset"
		bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
		postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
			"clientId": clientID,
			"data": map[string]interface{}{
				"clientId":       clientID,
				"conversationId": conversationID,
				"windows": []interface{}{map[string]interface{}{
					"windowId":  "chat/new",
					"windowKey": "chat/new",
				}},
			},
		})

		firstParameters := map[string]interface{}{"reportStarterId": "Alpha Report"}
		secondParameters := map[string]interface{}{"reportStarterId": "Missing Report"}
		input := &OpenInput{
			ClientID:  clientID,
			TimeoutMs: 50,
			Items: []OpenItem{
				{ID: "reportA", Parameters: firstParameters},
				{ID: "reportB", Parameters: secondParameters},
			},
		}
		output := &OpenOutput{}
		ctx := runtimerequestctx.WithConversationID(context.Background(), conversationID)
		viewSvc := New(repo.New(afs.New()), bridge)
		err := viewSvc.open(ctx, input, output)
		if err == nil || !strings.Contains(err.Error(), `unknown reportStarterId "Missing Report"`) ||
			!strings.Contains(err.Error(), `"Beta Report"`) ||
			strings.Contains(err.Error(), "beta_id") {
			t.Fatalf("expected actionable later-item error, got %v", err)
		}
		if len(output.Items) != 0 {
			t.Fatalf("batch must not contain partially opened results: %#v", output.Items)
		}
		if firstParameters["reportStarterId"] != "Alpha Report" ||
			secondParameters["reportStarterId"] != "Missing Report" {
			t.Fatalf("batch validation mutated caller parameters: %#v", input.Items)
		}
		if events := viewSvc.reg.ListEvents(conversationID, clientID, "", "", 10, 0); len(events) != 0 {
			t.Fatalf("rejected batch must not record an event: %#v", events)
		}
		assertNoUICommand(t, bridge, clientID)
	})
}

func TestOpenReturnsWindowIdVisibleToWindowList(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "forecastingCubeBuilder.yaml"), `
id: forecastingCubeBuilder
title: Forecasting
windowKey: forecastingCubeBuilder
presentation: hosted
region: chat.top
`)
		const conversationID = "conv-forecast"
		const clientID = "web-client-1"

		bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
		postUIRPC(t, bridge, "ui.hello", map[string]interface{}{"clientId": clientID})
		postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
			"clientId": clientID,
			"data": map[string]interface{}{
				"clientId":       clientID,
				"conversationId": conversationID,
				"selected": map[string]interface{}{
					"windowId": "chat/new",
					"tabId":    "chat/new",
				},
				"windows": []interface{}{
					map[string]interface{}{
						"windowId":       "chat/new",
						"windowKey":      "chat/new",
						"windowTitle":    "Chat",
						"conversationId": conversationID,
					},
				},
			},
		})
		waitForSnapshotEntry(t, bridge, clientID)

		commandDone := make(chan error, 1)
		go func() {
			result := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{"clientId": clientID, "timeoutMs": 1000})
			request, ok := result["params"].(map[string]interface{})
			if !ok {
				commandDone <- fmt.Errorf("expected command params, got %#v", result["params"])
				return
			}
			if got := request["method"]; got != "ui.window.open" {
				commandDone <- fmt.Errorf("expected ui.window.open command, got %#v", got)
				return
			}
			params, ok := request["params"].(map[string]interface{})
			if !ok {
				commandDone <- fmt.Errorf("expected params map, got %#v", request["params"])
				return
			}
			windowID := strings.TrimSpace(stringValue(params["windowId"]))
			windowKey := strings.TrimSpace(stringValue(params["windowKey"]))
			windowTitle := strings.TrimSpace(stringValue(params["windowTitle"]))
			if windowID == "" || windowKey == "" {
				commandDone <- fmt.Errorf("expected window id/key in params, got %#v", params)
				return
			}
			postUIRPC(t, bridge, "ui.snapshot", map[string]interface{}{
				"clientId": clientID,
				"data": map[string]interface{}{
					"clientId": clientID,
					"selected": map[string]interface{}{
						"windowId": windowID,
						"tabId":    windowID,
					},
					"windows": []interface{}{
						map[string]interface{}{
							"windowId":       "chat/new",
							"windowKey":      "chat/new",
							"windowTitle":    "Chat",
							"conversationId": conversationID,
						},
						map[string]interface{}{
							"windowId":       windowID,
							"windowKey":      windowKey,
							"windowTitle":    windowTitle,
							"conversationId": conversationID,
							"presentation":   "hosted",
							"region":         "chat.top",
							"parentKey":      "chat/new",
						},
					},
				},
			})
			postUIRPC(t, bridge, "ui.response", map[string]interface{}{
				"id": request["id"],
				"ok": true,
				"result": map[string]interface{}{
					"windowId": windowID,
				},
			})
			commandDone <- nil
		}()

		ctx := runtimerequestctx.WithPreferredUIClientID(
			runtimerequestctx.WithConversationID(context.Background(), conversationID),
			clientID,
		)
		viewSvc := New(repo.New(afs.New()), bridge)
		openOut := &OpenOutput{}
		if err := viewSvc.open(ctx, &OpenInput{ID: "forecastingCubeBuilder", TimeoutMs: 2_000}, openOut); err != nil {
			t.Fatalf("open failed: %v", err)
		}
		if err := <-commandDone; err != nil {
			t.Fatalf("bridge command handling failed: %v", err)
		}
		if openOut.WindowID == "" {
			t.Fatalf("expected open to return window id")
		}

		windowSvc := windowtool.New(bridge)
		listMethod, err := windowSvc.Method("list")
		if err != nil {
			t.Fatalf("resolve window list method: %v", err)
		}
		listOut := &windowtool.ListOutput{}
		if err := listMethod(ctx, &windowtool.ListInput{ClientID: clientID}, listOut); err != nil {
			t.Fatalf("window list failed: %v", err)
		}
		for _, item := range listOut.Items {
			if item.WindowID == openOut.WindowID {
				return
			}
		}
		t.Fatalf("open returned %q, but list returned %#v", openOut.WindowID, listOut.Items)
	})
}

func TestComputeWindowID_HostedViewsAreConversationScoped(t *testing.T) {
	item := &ListItem{
		WindowKey:    "order",
		Presentation: "hosted",
		Region:       "chat.top",
	}
	parameters := map[string]interface{}{
		"RecordId": []interface{}{2656980.0},
	}
	got := computeWindowID("order", parameters, "conv-1", item)
	expected := "order_" + fmt.Sprint(generateIntHash(parameters)) + "__conv-1"
	if got != expected {
		t.Fatalf("unexpected hosted window id: %s", got)
	}
}

func TestComputeWindowID_NonHostedViewsRemainUnscoped(t *testing.T) {
	item := &ListItem{
		WindowKey: "schedule",
	}
	got := computeWindowID("schedule", nil, "conv-1", item)
	if got != "schedule" {
		t.Fatalf("unexpected non-hosted window id: %s", got)
	}
}

func TestShouldRefreshOpenedWindow(t *testing.T) {
	if shouldRefreshOpenedWindow(nil, "win-1") {
		t.Fatalf("did not expect nil item to refresh")
	}
	if shouldRefreshOpenedWindow(&ListItem{Capabilities: viewproto.Capabilities{Datasource: true}}, "") {
		t.Fatalf("did not expect empty window id to refresh")
	}
	if shouldRefreshOpenedWindow(&ListItem{Presentation: "hosted"}, "win-1") {
		t.Fatalf("did not expect hosted view without datasource capability to refresh")
	}
	if shouldRefreshOpenedWindow(&ListItem{Presentation: "tab", Capabilities: viewproto.Capabilities{Datasource: true}}, "win-1") {
		t.Fatalf("did not expect non-hosted datasource view to refresh")
	}
	if !shouldRefreshOpenedWindow(&ListItem{Presentation: "hosted", Capabilities: viewproto.Capabilities{Datasource: true}}, "win-1") {
		t.Fatalf("expected hosted datasource view to refresh")
	}
}

func TestClientNamespaceFromSnapshotsUsesExactClient(t *testing.T) {
	clients := []uireg.ClientSnapshot{
		{ClientID: "web-client", Namespace: "web-ns"},
		{ClientID: "mobile-client", Namespace: "mobile-ns"},
	}

	if got := clientNamespaceFromSnapshots(clients, " mobile-client "); got != "mobile-ns" {
		t.Fatalf("expected mobile namespace, got %q", got)
	}
}

func TestClientNamespaceFromSnapshotsReturnsEmptyForMissingClient(t *testing.T) {
	clients := []uireg.ClientSnapshot{
		{ClientID: "web-client", Namespace: "web-ns"},
	}

	if got := clientNamespaceFromSnapshots(clients, "mobile-client"); got != "" {
		t.Fatalf("expected no namespace for missing client, got %q", got)
	}
}

func assertNestedValue(t *testing.T, holder map[string]interface{}, expected interface{}, parts ...string) {
	t.Helper()
	current := interface{}(holder)
	for _, part := range parts {
		asMap, ok := current.(map[string]interface{})
		if !ok {
			t.Fatalf("expected map at %q, got %#v", part, current)
		}
		current, ok = asMap[part]
		if !ok {
			t.Fatalf("missing nested key %q in %#v", part, asMap)
		}
	}
	if fmt.Sprintf("%#v", current) != fmt.Sprintf("%#v", expected) {
		t.Fatalf("unexpected bound value: got=%#v want=%#v", current, expected)
	}
}

func withWorkspaceRoot(t *testing.T, body func(root string)) {
	t.Helper()
	prev := workspace.Root()
	root := t.TempDir()
	workspace.SetRoot(root)
	t.Cleanup(func() {
		workspace.SetRoot(prev)
	})
	body(root)
}

func waitForSnapshotEntry(t *testing.T, bridge *forgeuisvc.Service, clientID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, entry := range bridge.Hub().SnapshotEntries() {
			if entry.ClientID == clientID {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("snapshot for client %q did not become visible", clientID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertNoUICommand(t *testing.T, bridge *forgeuisvc.Service, clientID string) {
	t.Helper()
	result := postUIRPC(t, bridge, "ui.poll", map[string]interface{}{
		"clientId":  clientID,
		"timeoutMs": 5,
	})
	if len(result) != 0 {
		t.Fatalf("expected no queued UI command, got %#v", result)
	}
}

func postUIRPC(t *testing.T, bridge *forgeuisvc.Service, method string, params map[string]interface{}) map[string]interface{} {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "test-" + method,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatalf("marshal rpc request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	bridge.Hub().ServeHTTPRPC(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()
	var envelope map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode %s response: %v", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("%s returned HTTP %d: %#v", method, resp.StatusCode, envelope)
	}
	if errValue := envelope["error"]; errValue != nil {
		t.Fatalf("%s returned RPC error: %#v", method, errValue)
	}
	result, _ := envelope["result"].(map[string]interface{})
	return result
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimLeft(contents, "\n")), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
