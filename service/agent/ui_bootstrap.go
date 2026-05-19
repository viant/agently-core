package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	viewproto "github.com/viant/agently-core/protocol/ui/view"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	forgewindowrepo "github.com/viant/agently-core/workspace/repository/forgewindow"
)

func (s *Service) workspaceUIBootstrap(ctx context.Context, conversationID string) string {
	if s == nil {
		return ""
	}
	var lines []string
	if views := s.workspaceUIViewSummaries(ctx); len(views) > 0 {
		lines = append(lines, "Available workspace UI views:")
		lines = append(lines, views...)
	}
	if live := s.workspaceUILiveSummaries(ctx, conversationID); len(live) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "Current live workspace state for this conversation:")
		lines = append(lines, live...)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
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
	client := items[0]
	if client.Snapshot == nil {
		return nil
	}
	var summaries []string
	if selected := strings.TrimSpace(client.Snapshot.Selected.WindowID); selected != "" {
		summaries = append(summaries, fmt.Sprintf("- selectedWindowId=%s clientId=%s", selected, strings.TrimSpace(client.ClientID)))
	}
	for _, win := range client.Snapshot.Windows {
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
					tabs = append(tabs, label)
				}
			}
			sort.Strings(tabs)
			if len(tabs) > 0 {
				summary += " tabs=" + strings.Join(tabs, ",")
			}
			var controls []string
			for _, control := range surface.Controls {
				label := firstNonEmpty(strings.TrimSpace(control.Label), strings.TrimSpace(control.ID))
				if label != "" {
					controls = append(controls, label)
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
		summaries = append(summaries, summary)
	}
	if events := s.uiRegistry.ListEvents(strings.TrimSpace(conversationID), strings.TrimSpace(client.ClientID), "", "", 10, 0); len(events) > 0 {
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
