package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	viewproto "github.com/viant/agently-core/protocol/ui/view"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uisvc "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/reporting/catalog"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	forgewindowrepo "github.com/viant/agently-core/workspace/repository/forgewindow"
)

func (s *Service) workspaceUIBootstrap(ctx context.Context, conversationID string) string {
	if s == nil {
		return ""
	}
	// One advisory metadata batch reuses the host's existing actor/revision
	// snapshot across view admission and the nested report catalog read.
	readCtx, finish, err := uisvc.BeginMetadataReadScope(ctx, s.uiBridge.MetadataScope())
	if err != nil {
		return ""
	}
	ctx = readCtx
	var lines []string
	if views := s.workspaceUIViewSummaries(ctx); len(views) > 0 {
		lines = append(lines, "Available workspace UI views:")
		lines = append(lines, views...)
	}
	if reports := s.workspaceUIReportSummaries(ctx); len(reports) > 0 {
		lines = append(lines, "Published report resources (metadata visibility only; opening and execution reauthorize):")
		lines = append(lines, reports...)
	}
	if live := s.workspaceUILiveSummaries(ctx, conversationID); len(live) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "Current live workspace state for this conversation:")
		lines = append(lines, live...)
	}
	if err := uisvc.FinishMetadataReadScope(finish, nil); err != nil {
		return ""
	}
	if len(lines) == 0 {
		return ""
	}
	lines = append(lines, "", "UI capability contract: Metadata discovers declarative windows, tab hierarchy, forms, field constraints/options and supported actions without running them (ui/metadata:list|get when advertised). Read inspects current UI observations (ui/context:get; scoped datasource peek). Operate uses only advertised MCP UI actions on the exact current window. Discover metadata before operating; UI state is observation, never permission authority.")
	return strings.Join(lines, "\n")
}

// Report identities come from the same managing catalog used by report opening.
// They are metadata hints, not execution grants or window-definition aliases.
func (s *Service) workspaceUIReportSummaries(ctx context.Context) []string {
	if s == nil || s.uiBridge == nil || !s.uiBridge.HasReportCatalog() {
		return nil
	}
	result, err := s.uiBridge.ReportCatalog(ctx, &catalog.ReportCatalogQuery{Limit: 1000})
	if err != nil || result == nil {
		return nil
	}
	lines := make([]string, 0, len(result.Reports))
	for _, entry := range result.Reports {
		lines = append(lines, fmt.Sprintf("- resourceUri=%s title=%q namespace=%s open=%t", entry.URI, entry.Title, entry.Namespace, entry.Capabilities.Open))
	}
	sort.Strings(lines)
	if result.NextCursor != "" {
		lines = append(lines, "- Additional report resources are available through the report catalog.")
	}
	return lines
}

func (s *Service) workspaceUIViewSummaries(ctx context.Context) []string {
	if s == nil || s.fs == nil {
		return nil
	}
	repo := forgewindowrepo.New(s.fs)
	items, err := repo.LoadAll(ctx)
	if err != nil || len(items) == 0 {
		return nil
	}
	summaries := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil || strings.TrimSpace(item.ID) == "" {
			continue
		}
		if s.uiBridge != nil && s.uiBridge.AuthzReady() {
			allowed, err := s.uiBridge.WindowAuthorize(ctx, strings.TrimSpace(item.WindowKey))
			if err != nil || !allowed {
				continue
			}
		}
		summaries = append(summaries, summarizeWorkspaceView(item))
	}
	sort.Strings(summaries)
	return summaries
}

func summarizeWorkspaceView(item *viewproto.Spec) string {
	if item == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("- %s", strings.TrimSpace(item.ID))}
	if title := strings.TrimSpace(item.Title); title != "" {
		parts = append(parts, fmt.Sprintf("title=%q", title))
	}
	if key := strings.TrimSpace(item.WindowKey); key != "" {
		parts = append(parts, fmt.Sprintf("windowKey=%s", key))
	}
	if presentation := strings.TrimSpace(item.Presentation); presentation != "" {
		parts = append(parts, fmt.Sprintf("presentation=%s", presentation))
	}
	if len(item.Parameters) > 0 {
		required := make([]string, 0, len(item.Parameters))
		for _, parameter := range item.Parameters {
			if parameter.Required {
				required = append(required, strings.TrimSpace(parameter.Name))
			}
		}
		sort.Strings(required)
		if len(required) > 0 {
			parts = append(parts, fmt.Sprintf("required=%s", strings.Join(required, ",")))
		}
	}
	return strings.Join(parts, " ")
}

func (s *Service) workspaceUILiveSummaries(ctx context.Context, conversationID string) []string {
	if s == nil || s.uiRegistry == nil {
		return nil
	}
	items, err := s.uiRegistry.ListByConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil || len(items) == 0 {
		return nil
	}
	client := selectWorkspaceUIBootstrapClient(items, runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	if client == nil || client.Snapshot == nil {
		return nil
	}
	var summaries []string
	if selected := strings.TrimSpace(client.Snapshot.Selected.WindowID); selected != "" {
		for _, win := range client.Snapshot.Windows {
			if win.WindowID == selected && (win.ConversationID == conversationID || (win.WindowID == "chat/new" && client.Snapshot.ConversationID == conversationID)) {
				summaries = append(summaries, fmt.Sprintf("- selectedWindowId=%s clientId=%s", selected, strings.TrimSpace(client.ClientID)))
				break
			}
		}
	}
	if current := summarizeWorkspaceCurrentUI(client, conversationID); current != "" {
		summaries = append(summaries, current)
	}
	for _, win := range client.Snapshot.Windows {
		if win.ConversationID != "" && win.ConversationID != strings.TrimSpace(conversationID) {
			continue
		}
		summary := fmt.Sprintf("- windowId=%s windowKey=%s title=%q", strings.TrimSpace(win.WindowID), strings.TrimSpace(win.WindowKey), strings.TrimSpace(win.WindowTitle))
		if len(win.Parameters) > 0 {
			if compact := compactWindowParametersForBootstrap(win.Parameters); compact != "" {
				summary += " parameters=" + compact
			}
		}
		if surface := uireg.BuildWindowSurface(&win); surface != nil {
			var tabs []string
			for _, tab := range surface.Tabs {
				label := firstNonEmpty(strings.TrimSpace(tab.Title), strings.TrimSpace(tab.TabID))
				if label != "" {
					tabs = append(tabs, fmt.Sprintf("{tabId:%q,containerId:%q,title:%q}", tab.TabID, tab.ContainerID, label))
				}
			}
			sort.Strings(tabs)
			if len(tabs) > 0 {
				summary += " tabs=" + strings.Join(tabs, ",")
			}
			var controls []string
			for _, control := range surface.Controls {
				if summary := summarizeWorkspaceControl(control); summary != "" {
					controls = append(controls, summary)
				}
			}
			sort.Strings(controls)
			if len(controls) > 0 {
				summary += " controls=" + strings.Join(controls, ",")
			}
		}
		if refs := uireg.ListDataSourceRefs(&win); len(refs) > 0 {
			summary += " datasources=" + strings.Join(refs, ",")
		}
		if win.WindowID == client.Snapshot.Selected.WindowID {
			if targets := summarizeWorkspaceSelectionTargets(&win); targets != "" {
				summary += " selectionTargets=" + targets
			}
		}
		summaries = append(summaries, summary)
	}
	if events, err := s.uiRegistry.ListEventsContext(ctx, strings.TrimSpace(conversationID), strings.TrimSpace(client.ClientID), "", "", 10, 0); err == nil && len(events) > 0 {
		summaries = append(summaries, "- recentEvents:")
		for _, event := range events {
			line := fmt.Sprintf("  - #%d kind=%s actor=%s", event.Seq, strings.TrimSpace(event.Kind), strings.TrimSpace(event.Actor))
			if windowKey := strings.TrimSpace(event.WindowKey); windowKey != "" {
				line += " windowKey=" + windowKey
			}
			summaries = append(summaries, line)
		}
	}
	return summaries
}

// A bounded set of live row identities lets intake handle a uniquely named
// selection in one tool call. The snapshot is already scoped to this client and
// conversation; do not forward complete rows or unrelated datasource fields.
func summarizeWorkspaceSelectionTargets(win *uireg.WindowSnapshot) string {
	if win == nil {
		return ""
	}
	refs := make([]string, 0, len(win.DataSources))
	for ref := range win.DataSources {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	targets := make([]map[string]interface{}, 0, 12)
	for _, ref := range refs {
		rows, ok := win.DataSources[ref].Collection.([]interface{})
		if !ok {
			continue
		}
		added := 0
		for _, raw := range rows {
			row, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			name, ok := row["name"].(string)
			if !ok || strings.TrimSpace(name) == "" || row["id"] == nil {
				continue
			}
			if len(name) > 80 {
				name = name[:80]
			}
			targets = append(targets, map[string]interface{}{
				"dataSourceRef": ref, "identityFields": []string{"id"},
				"id": row["id"], "name": name,
			})
			added++
			if added == 3 || len(targets) == 12 {
				break
			}
		}
		if len(targets) == 12 {
			break
		}
	}
	if len(targets) == 0 {
		return ""
	}
	encoded, err := json.Marshal(targets)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func summarizeWorkspaceControl(control uireg.SurfaceControl) string {
	id := strings.TrimSpace(control.ID)
	if id == "" {
		return ""
	}
	summary := id
	if label := strings.TrimSpace(control.Label); label != "" && !strings.EqualFold(label, id) {
		summary += "(" + label + ")"
	}
	if scope := strings.TrimSpace(control.Scope); scope != "" {
		summary += ":" + scope
	}
	if len(control.Options) > 0 {
		options := make([]string, 0, len(control.Options))
		for _, option := range control.Options {
			value := strings.TrimSpace(fmt.Sprint(option.Value))
			if value == "" || value == "<nil>" {
				continue
			}
			if label := strings.TrimSpace(option.Label); label != "" && !strings.EqualFold(label, value) {
				value += ":" + label
			}
			options = append(options, value)
			if len(options) == 12 {
				break
			}
		}
		if len(options) > 0 {
			summary += "[" + strings.Join(options, "|") + "]"
		}
	}
	return summary
}

func selectWorkspaceUIBootstrapClient(items []uireg.ClientSnapshot, preferredClientID string) *uireg.ClientSnapshot {
	preferredClientID = strings.TrimSpace(preferredClientID)
	if preferredClientID != "" {
		for i := range items {
			if strings.TrimSpace(items[i].ClientID) == preferredClientID {
				return &items[i]
			}
		}
		return nil
	}
	for i := range items {
		if items[i].Snapshot != nil {
			return &items[i]
		}
	}
	return nil
}

func compactWindowParametersForBootstrap(parameters map[string]interface{}) string {
	if len(parameters) == 0 {
		return ""
	}
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, parameters[key]))
	}
	return strings.Join(parts, ";")
}

// Consume only the UI-exposed current-state contract, scoped to this request's
// chat and selected window. Browser observations never become authorization.
func summarizeWorkspaceCurrentUI(client *uireg.ClientSnapshot, conversationID string) string {
	if client == nil || client.Snapshot == nil || strings.TrimSpace(conversationID) == "" {
		return ""
	}
	snapshot := client.Snapshot
	for i := range snapshot.Windows {
		win := &snapshot.Windows[i]
		if win.WindowID != snapshot.Selected.WindowID {
			continue
		}
		if win.ConversationID != conversationID && !(win.WindowID == "chat/new" && snapshot.ConversationID == conversationID) {
			return ""
		}
		current := uireg.CurrentSemanticContext(win)
		if current == nil {
			return ""
		}
		encoded, err := json.Marshal(map[string]interface{}{"windowId": win.WindowID, "windowKey": win.WindowKey, "observedAt": snapshot.ObservedAt, "operationRevision": win.OperationRevision, "receivedAt": client.UpdatedAt.UnixMilli(), "state": current})
		if err != nil {
			return ""
		}
		return "- currentUIObservation=" + string(encoded)
	}
	return ""
}
