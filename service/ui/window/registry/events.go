package registry

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type SurfaceTab struct {
	ContainerID string `json:"containerId,omitempty"`
	TabID       string `json:"tabId,omitempty"`
	Title       string `json:"title,omitempty"`
	Selected    bool   `json:"selected,omitempty"`
}

type SurfaceControlOption struct {
	Value interface{} `json:"value,omitempty"`
	Label string      `json:"label,omitempty"`
}

type SurfaceControl struct {
	ID          string                 `json:"id,omitempty"`
	Label       string                 `json:"label,omitempty"`
	Type        string                 `json:"type,omitempty"`
	Scope       string                 `json:"scope,omitempty"`
	BindingPath string                 `json:"bindingPath,omitempty"`
	DataField   string                 `json:"dataField,omitempty"`
	Value       interface{}            `json:"value,omitempty"`
	Options     []SurfaceControlOption `json:"options,omitempty"`
}

type WindowSurface struct {
	Tabs     []SurfaceTab     `json:"tabs,omitempty"`
	Controls []SurfaceControl `json:"controls,omitempty"`
}

type UIEvent struct {
	Seq            int64                  `json:"seq"`
	At             time.Time              `json:"at"`
	ConversationID string                 `json:"conversationId,omitempty"`
	ClientID       string                 `json:"clientId,omitempty"`
	WindowID       string                 `json:"windowId,omitempty"`
	WindowKey      string                 `json:"windowKey,omitempty"`
	Kind           string                 `json:"kind,omitempty"`
	Actor          string                 `json:"actor,omitempty"`
	Detail         map[string]interface{} `json:"detail,omitempty"`
}

type sharedState struct {
	mu           sync.Mutex
	fingerprints map[string]string
	snapshots    map[string]*Snapshot
	events       map[string][]UIEvent
	nextSeq      int64
}

var sharedStates sync.Map

func sharedStateFor(bridge interface{}) *sharedState {
	key := fmt.Sprintf("%p", bridge)
	if state, ok := sharedStates.Load(key); ok {
		return state.(*sharedState)
	}
	state := &sharedState{
		fingerprints: map[string]string{},
		snapshots:    map[string]*Snapshot{},
		events:       map[string][]UIEvent{},
	}
	actual, _ := sharedStates.LoadOrStore(key, state)
	return actual.(*sharedState)
}

func stateSnapshotKey(ns, clientID string) string {
	return strings.TrimSpace(ns) + "::" + strings.TrimSpace(clientID)
}

func cloneSnapshot(in *Snapshot) *Snapshot {
	if in == nil {
		return nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out Snapshot
	if err := json.Unmarshal(data, &out); err != nil {
		return in
	}
	return &out
}

func (s *sharedState) ingestSnapshot(ns, clientID string, snap *Snapshot, raw json.RawMessage, updatedAt time.Time) {
	if s == nil || snap == nil {
		return
	}
	key := stateSnapshotKey(ns, clientID)
	fingerprint := string(raw)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fingerprints[key] == fingerprint {
		return
	}
	prev := s.snapshots[key]
	s.fingerprints[key] = fingerprint
	s.snapshots[key] = cloneSnapshot(snap)

	nextSeq := func() int64 {
		s.nextSeq++
		return s.nextSeq
	}
	record := func(event UIEvent) {
		event.Seq = nextSeq()
		if event.At.IsZero() {
			event.At = updatedAt
		}
		s.events[key] = append(s.events[key], event)
		if len(s.events[key]) > 200 {
			s.events[key] = append([]UIEvent(nil), s.events[key][len(s.events[key])-200:]...)
		}
	}

	if prev == nil {
		return
	}

	prevWindows := map[string]WindowSnapshot{}
	for _, win := range prev.Windows {
		prevWindows[strings.TrimSpace(win.WindowID)] = win
	}
	nextWindows := map[string]WindowSnapshot{}
	for _, win := range snap.Windows {
		nextWindows[strings.TrimSpace(win.WindowID)] = win
	}

	for id, win := range nextWindows {
		if id == "" {
			continue
		}
		if _, ok := prevWindows[id]; !ok {
			record(UIEvent{
				ConversationID: strings.TrimSpace(win.ConversationID),
				ClientID:       strings.TrimSpace(clientID),
				WindowID:       id,
				WindowKey:      strings.TrimSpace(win.WindowKey),
				Kind:           "window.opened",
				Actor:          "user",
				Detail: map[string]interface{}{
					"windowTitle":  strings.TrimSpace(win.WindowTitle),
					"presentation": strings.TrimSpace(win.Presentation),
					"region":       strings.TrimSpace(win.Region),
				},
			})
		}
	}
	for id, win := range prevWindows {
		if id == "" {
			continue
		}
		if _, ok := nextWindows[id]; !ok {
			record(UIEvent{
				ConversationID: strings.TrimSpace(win.ConversationID),
				ClientID:       strings.TrimSpace(clientID),
				WindowID:       id,
				WindowKey:      strings.TrimSpace(win.WindowKey),
				Kind:           "window.closed",
				Actor:          "user",
			})
		}
	}

	if strings.TrimSpace(prev.Selected.WindowID) != strings.TrimSpace(snap.Selected.WindowID) {
		nextWin := nextWindows[strings.TrimSpace(snap.Selected.WindowID)]
		record(UIEvent{
			ConversationID: strings.TrimSpace(nextWin.ConversationID),
			ClientID:       strings.TrimSpace(clientID),
			WindowID:       strings.TrimSpace(snap.Selected.WindowID),
			WindowKey:      strings.TrimSpace(nextWin.WindowKey),
			Kind:           "window.focused",
			Actor:          "user",
			Detail: map[string]interface{}{
				"fromWindowId": strings.TrimSpace(prev.Selected.WindowID),
				"toWindowId":   strings.TrimSpace(snap.Selected.WindowID),
			},
		})
	}

	for id, win := range nextWindows {
		prevWin, ok := prevWindows[id]
		if !ok {
			continue
		}
		prevTabs := viewStateTabs(prevWin.ViewState)
		nextTabs := viewStateTabs(win.ViewState)
		for containerID, nextTab := range nextTabs {
			if strings.TrimSpace(nextTab) == "" {
				continue
			}
			if prevTabs[containerID] == nextTab {
				continue
			}
			record(UIEvent{
				ConversationID: strings.TrimSpace(win.ConversationID),
				ClientID:       strings.TrimSpace(clientID),
				WindowID:       id,
				WindowKey:      strings.TrimSpace(win.WindowKey),
				Kind:           "tab.selected",
				Actor:          "user",
				Detail: map[string]interface{}{
					"containerId": containerID,
					"fromTabId":   prevTabs[containerID],
					"toTabId":     nextTab,
				},
			})
		}
	}
}

func (s *sharedState) recordEvent(ns, clientID string, event UIEvent) {
	if s == nil {
		return
	}
	key := stateSnapshotKey(ns, clientID)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSeq++
	event.Seq = s.nextSeq
	if event.At.IsZero() {
		event.At = time.Now()
	}
	s.events[key] = append(s.events[key], event)
	if len(s.events[key]) > 200 {
		s.events[key] = append([]UIEvent(nil), s.events[key][len(s.events[key])-200:]...)
	}
}

func (s *sharedState) listEvents(ns, clientID string) []UIEvent {
	if s == nil {
		return nil
	}
	key := stateSnapshotKey(ns, clientID)
	s.mu.Lock()
	defer s.mu.Unlock()
	events := s.events[key]
	if len(events) == 0 {
		return nil
	}
	out := make([]UIEvent, len(events))
	copy(out, events)
	return out
}

func BuildWindowSurface(win *WindowSnapshot) *WindowSurface {
	if win == nil {
		return nil
	}
	viewMeta, _ := mapValue(win.Metadata, "view")
	tabsRaw, _ := sliceValue(viewMeta, "tabs")
	controlsRaw, _ := sliceValue(viewMeta, "controls")
	viewTabs := viewStateTabs(win.ViewState)

	surface := &WindowSurface{}
	for _, raw := range tabsRaw {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		containerID := strings.TrimSpace(stringValue(entry["containerId"]))
		tabID := strings.TrimSpace(stringValue(entry["tabId"]))
		if tabID == "" {
			continue
		}
		surface.Tabs = append(surface.Tabs, SurfaceTab{
			ContainerID: containerID,
			TabID:       tabID,
			Title:       strings.TrimSpace(stringValue(entry["title"])),
			Selected:    containerID != "" && viewTabs[containerID] == tabID,
		})
	}
	sort.SliceStable(surface.Tabs, func(i, j int) bool {
		if surface.Tabs[i].ContainerID != surface.Tabs[j].ContainerID {
			return surface.Tabs[i].ContainerID < surface.Tabs[j].ContainerID
		}
		return surface.Tabs[i].TabID < surface.Tabs[j].TabID
	})

	for _, raw := range controlsRaw {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		id := strings.TrimSpace(stringValue(entry["id"]))
		if id == "" {
			continue
		}
		scope := strings.TrimSpace(stringValue(entry["scope"]))
		bindingPath := strings.TrimSpace(stringValue(entry["bindingPath"]))
		dataField := strings.TrimSpace(stringValue(entry["dataField"]))
		surface.Controls = append(surface.Controls, SurfaceControl{
			ID:          id,
			Label:       strings.TrimSpace(stringValue(entry["label"])),
			Type:        strings.TrimSpace(stringValue(entry["type"])),
			Scope:       scope,
			BindingPath: bindingPath,
			DataField:   dataField,
			Value:       controlValue(win.WindowForm, scope, bindingPath, dataField, id),
			Options:     controlOptions(entry["options"]),
		})
	}
	sort.SliceStable(surface.Controls, func(i, j int) bool {
		return surface.Controls[i].ID < surface.Controls[j].ID
	})
	if len(surface.Tabs) == 0 && len(surface.Controls) == 0 {
		return nil
	}
	return surface
}

func controlValue(windowForm map[string]interface{}, scope, bindingPath, dataField, id string) interface{} {
	if !strings.EqualFold(strings.TrimSpace(scope), "windowForm") {
		return nil
	}
	switch {
	case bindingPath != "":
		return resolvePath(windowForm, bindingPath)
	case dataField != "":
		return resolvePath(windowForm, dataField)
	case id != "":
		return resolvePath(windowForm, id)
	default:
		return nil
	}
}

func controlOptions(raw interface{}) []SurfaceControlOption {
	list, ok := raw.([]interface{})
	if !ok || len(list) == 0 {
		return nil
	}
	result := make([]SurfaceControlOption, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		result = append(result, SurfaceControlOption{
			Value: entry["value"],
			Label: strings.TrimSpace(stringValue(entry["label"])),
		})
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func ListDataSourceRefs(win *WindowSnapshot) []string {
	if win == nil || len(win.DataSources) == 0 {
		return nil
	}
	refs := make([]string, 0, len(win.DataSources))
	for ref := range win.DataSources {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

func mapValue(source map[string]interface{}, key string) (map[string]interface{}, bool) {
	if len(source) == 0 {
		return nil, false
	}
	raw, ok := source[key]
	if !ok {
		return nil, false
	}
	value, ok := raw.(map[string]interface{})
	return value, ok
}

func sliceValue(source map[string]interface{}, key string) ([]interface{}, bool) {
	if len(source) == 0 {
		return nil, false
	}
	raw, ok := source[key]
	if !ok {
		return nil, false
	}
	value, ok := raw.([]interface{})
	return value, ok
}

func viewStateTabs(viewState map[string]interface{}) map[string]string {
	result := map[string]string{}
	raw, ok := viewState["tabs"]
	if !ok {
		return result
	}
	tabs, ok := raw.(map[string]interface{})
	if !ok {
		return result
	}
	for key, value := range tabs {
		id := strings.TrimSpace(stringValue(value))
		if key == "" || id == "" {
			continue
		}
		result[key] = id
	}
	return result
}

func stringValue(v interface{}) string {
	if v == nil {
		return ""
	}
	switch actual := v.(type) {
	case string:
		return actual
	default:
		return fmt.Sprintf("%v", v)
	}
}

func resolvePath(source map[string]interface{}, path string) interface{} {
	current := interface{}(source)
	for _, part := range strings.Split(strings.TrimSpace(path), ".") {
		if part == "" {
			continue
		}
		asMap, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		next, exists := asMap[part]
		if !exists {
			return nil
		}
		current = next
	}
	return current
}
