package context

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

const Name = "ui/context"

type GetInput struct {
	ClientID  string `json:"clientId,omitempty"`
	WindowID  string `json:"windowId,omitempty"`
	WindowKey string `json:"windowKey,omitempty"`
}

type WindowContext struct {
	Window         *uireg.WindowSnapshot `json:"window,omitempty"`
	DataSourceRefs []string              `json:"dataSourceRefs,omitempty"`
	Surface        *uireg.WindowSurface  `json:"surface,omitempty"`
}

type GetOutput struct {
	ConversationID  string                  `json:"conversationId,omitempty"`
	ClientID        string                  `json:"clientId,omitempty"`
	FocusedWindowID string                  `json:"focusedWindowId,omitempty"`
	Selected        *uireg.SnapshotSelected `json:"selected,omitempty"`
	Windows         []WindowContext         `json:"windows,omitempty"`
	RecentEvents    []uireg.UIEvent         `json:"recentEvents,omitempty"`
	CurrentReports  []CurrentReportContext  `json:"currentReports,omitempty"`
}

type CurrentReportContext struct {
	WindowID     string                 `json:"windowId,omitempty"`
	WindowKey    string                 `json:"windowKey,omitempty"`
	ReportName   string                 `json:"reportName,omitempty"`
	ReportID     string                 `json:"reportId,omitempty"`
	ArtifactRef  string                 `json:"artifactRef,omitempty"`
	SourceKind   string                 `json:"sourceKind,omitempty"`
	Format       string                 `json:"format,omitempty"`
	Filters      map[string]interface{} `json:"filters,omitempty"`
	ContextEvent *uireg.UIEvent         `json:"contextEvent,omitempty"`
	LatestRun    *uireg.UIEvent         `json:"latestRun,omitempty"`
	LatestExport *uireg.UIEvent         `json:"latestExport,omitempty"`
}

type Service struct {
	reg *uireg.Registry
}

func New(bridge *forgeuisvc.Service) *Service {
	return &Service{reg: uireg.New(bridge)}
}

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "get", Description: "Return the combined current-conversation workspace UI snapshot including visible windows, surfaces, datasource refs, and recent events.", Input: reflect.TypeOf(&GetInput{}), Output: reflect.TypeOf(&GetOutput{})},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "get":
		return s.get, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

func (s *Service) get(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*GetInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*GetOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	preferredClientID := normalizeOptionalClientID(input.ClientID)
	items, err := s.reg.ListReadableByConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if preferredClientID != "" {
		filtered := make([]uireg.ClientSnapshot, 0, len(items))
		for _, item := range items {
			if strings.TrimSpace(item.ClientID) == preferredClientID {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if len(items) == 0 {
		output.ConversationID = conversationID
		output.ClientID = preferredClientID
		output.RecentEvents = newestFirstUIEvents(
			s.reg.ListEvents(conversationID, preferredClientID, strings.TrimSpace(input.WindowID), strings.TrimSpace(input.WindowKey), 10, 0),
		)
		output.CurrentReports = buildCurrentReportContexts(
			s.reg.ListEvents(conversationID, preferredClientID, strings.TrimSpace(input.WindowID), strings.TrimSpace(input.WindowKey), 100, 0),
		)
		return nil
	}
	client := items[0]
	output.ConversationID = conversationID
	output.ClientID = client.ClientID
	if client.Snapshot != nil {
		output.FocusedWindowID = strings.TrimSpace(client.Snapshot.Selected.WindowID)
		selected := client.Snapshot.Selected
		output.Selected = &selected
		for _, win := range client.Snapshot.Windows {
			windowCopy := win
			if strings.TrimSpace(input.WindowID) != "" && strings.TrimSpace(win.WindowID) != strings.TrimSpace(input.WindowID) {
				continue
			}
			if strings.TrimSpace(input.WindowID) == "" && strings.TrimSpace(input.WindowKey) != "" && strings.TrimSpace(win.WindowKey) != strings.TrimSpace(input.WindowKey) {
				continue
			}
			windowCopy.DataSources = nil
			output.Windows = append(output.Windows, WindowContext{
				Window:         &windowCopy,
				DataSourceRefs: uireg.ListDataSourceRefs(&win),
				Surface:        uireg.BuildWindowSurface(&win),
			})
		}
	}
	output.RecentEvents = newestFirstUIEvents(
		s.reg.ListEvents(conversationID, client.ClientID, strings.TrimSpace(input.WindowID), strings.TrimSpace(input.WindowKey), 10, 0),
	)
	output.CurrentReports = buildCurrentReportContexts(
		s.reg.ListEvents(conversationID, client.ClientID, strings.TrimSpace(input.WindowID), strings.TrimSpace(input.WindowKey), 100, 0),
	)
	return nil
}

func buildCurrentReportContexts(events []uireg.UIEvent) []CurrentReportContext {
	type windowEvents struct {
		key    string
		events []uireg.UIEvent
	}
	grouped := map[string]*windowEvents{}
	for _, event := range newestFirstUIEvents(events) {
		if !isReportLifecycleEvent(event.Kind) {
			continue
		}
		key := reportEventWindowKey(event)
		if key == "" {
			continue
		}
		group := grouped[key]
		if group == nil {
			group = &windowEvents{key: key}
			grouped[key] = group
		}
		group.events = append(group.events, event)
	}

	groups := make([]*windowEvents, 0, len(grouped))
	for _, group := range grouped {
		groups = append(groups, group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return eventIsNewer(groups[i].events[0], groups[j].events[0])
	})

	result := make([]CurrentReportContext, 0, len(groups))
	for _, group := range groups {
		anchor := latestReportContextAnchor(group.events)
		if anchor == nil {
			continue
		}
		current := CurrentReportContext{
			WindowID:  anchor.WindowID,
			WindowKey: anchor.WindowKey,
		}
		applyReportEventDetail(&current, anchor.Detail)
		if strings.TrimSpace(anchor.Kind) == "report.context_updated" {
			anchorCopy := *anchor
			current.ContextEvent = &anchorCopy
		}
		for index := range group.events {
			event := group.events[index]
			if !reportEventMatchesAnchor(event, *anchor) {
				continue
			}
			switch strings.TrimSpace(event.Kind) {
			case "report.run":
				if current.LatestRun == nil && reportEventSucceeded(event, false) {
					eventCopy := event
					current.LatestRun = &eventCopy
				}
			case "report.export_complete":
				if current.LatestExport == nil && reportEventSucceeded(event, true) {
					eventCopy := event
					current.LatestExport = &eventCopy
				}
			}
		}
		result = append(result, current)
	}
	return result
}

func newestFirstUIEvents(events []uireg.UIEvent) []uireg.UIEvent {
	result := append([]uireg.UIEvent(nil), events...)
	sort.SliceStable(result, func(i, j int) bool { return eventIsNewer(result[i], result[j]) })
	return result
}

func eventIsNewer(left, right uireg.UIEvent) bool {
	if left.Seq != right.Seq {
		return left.Seq > right.Seq
	}
	return left.At.After(right.At)
}

func isReportLifecycleEvent(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "report.context_updated", "report.run", "report.run_start", "report.export_complete", "report.export_start":
		return true
	default:
		return false
	}
}

func reportEventWindowKey(event uireg.UIEvent) string {
	if value := strings.TrimSpace(event.WindowID); value != "" {
		return value
	}
	return strings.TrimSpace(event.WindowKey)
}

func latestReportContextAnchor(events []uireg.UIEvent) *uireg.UIEvent {
	for index := range events {
		if strings.TrimSpace(events[index].Kind) == "report.context_updated" {
			return &events[index]
		}
	}
	if len(events) > 0 {
		return &events[0]
	}
	return nil
}

func reportEventMatchesAnchor(event, anchor uireg.UIEvent) bool {
	if reportEventWindowKey(event) != reportEventWindowKey(anchor) {
		return false
	}
	identityKey := "artifactRef"
	if reportEventString(anchor.Detail, "reportId") != "" {
		identityKey = "reportId"
	} else if reportEventString(anchor.Detail, "reportName") != "" {
		identityKey = "reportName"
	}
	if reportEventString(event.Detail, identityKey) != reportEventString(anchor.Detail, identityKey) {
		return false
	}
	return canonicalEventValue(event.Detail["filters"]) == canonicalEventValue(anchor.Detail["filters"])
}

func reportEventSucceeded(event uireg.UIEvent, requireArtifact bool) bool {
	status := strings.ToLower(reportEventString(event.Detail, "status"))
	if status != "" && status != "succeeded" && status != "success" && status != "completed" && status != "complete" {
		return false
	}
	return !requireArtifact || reportEventString(event.Detail, "artifactId") != ""
}

func reportEventString(detail map[string]interface{}, key string) string {
	if detail == nil {
		return ""
	}
	value, _ := detail[key].(string)
	return strings.TrimSpace(value)
}

func canonicalEventValue(value interface{}) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func applyReportEventDetail(current *CurrentReportContext, detail map[string]interface{}) {
	if current == nil || detail == nil {
		return
	}
	stringValue := func(key string) string {
		value, _ := detail[key].(string)
		return strings.TrimSpace(value)
	}
	if value := stringValue("reportName"); value != "" {
		current.ReportName = value
	}
	if value := stringValue("reportId"); value != "" {
		current.ReportID = value
	}
	if value := stringValue("artifactRef"); value != "" {
		current.ArtifactRef = value
	}
	if value := stringValue("sourceKind"); value != "" {
		current.SourceKind = value
	}
	if value := stringValue("format"); value != "" {
		current.Format = value
	}
	if filters, ok := detail["filters"].(map[string]interface{}); ok {
		current.Filters = filters
	}
}

func normalizeOptionalClientID(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "default") {
		return ""
	}
	return value
}
