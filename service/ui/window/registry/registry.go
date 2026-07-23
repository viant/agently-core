package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

type Registry struct {
	bridge *forgeuisvc.Service
	state  *sharedState
}

const defaultSnapshotFreshness = 15 * time.Second
const defaultPollFreshness = 12 * time.Second

func New(bridge *forgeuisvc.Service) *Registry {
	return &Registry{bridge: bridge, state: sharedStateFor(bridge)}
}

type Snapshot struct {
	ConversationID string           `json:"conversationId,omitempty"`
	ClientID       string           `json:"clientId,omitempty"`
	Selected       SnapshotSelected `json:"selected,omitempty"`
	Windows        []WindowSnapshot `json:"windows,omitempty"`
}

type SnapshotSelected struct {
	WindowID string `json:"windowId,omitempty"`
	TabID    string `json:"tabId,omitempty"`
}

type WindowSnapshot struct {
	WindowID           string                        `json:"windowId,omitempty"`
	WindowKey          string                        `json:"windowKey,omitempty"`
	WindowTitle        string                        `json:"windowTitle,omitempty"`
	ConversationID     string                        `json:"conversationId,omitempty"`
	Presentation       string                        `json:"presentation,omitempty"`
	Region             string                        `json:"region,omitempty"`
	ParentKey          string                        `json:"parentKey,omitempty"`
	WorkspaceSharePct  int                           `json:"workspaceSharePct,omitempty"`
	WorkspaceMinHeight int                           `json:"workspaceMinHeight,omitempty"`
	CompareContext     map[string]interface{}        `json:"compareContext,omitempty"`
	Parameters         map[string]interface{}        `json:"parameters,omitempty"`
	WindowForm         map[string]interface{}        `json:"windowForm,omitempty"`
	ViewState          map[string]interface{}        `json:"viewState,omitempty"`
	Metadata           map[string]interface{}        `json:"metadata,omitempty"`
	InTab              bool                          `json:"inTab,omitempty"`
	IsModal            bool                          `json:"isModal,omitempty"`
	IsMinimized        bool                          `json:"isMinimized,omitempty"`
	DataSources        map[string]DataSourceSnapshot `json:"dataSources,omitempty"`
}

type DataSourceSnapshot struct {
	DataSourceRef  string                 `json:"dataSourceRef,omitempty"`
	Input          map[string]interface{} `json:"input,omitempty"`
	Filter         map[string]interface{} `json:"filter,omitempty"`
	Control        map[string]interface{} `json:"control,omitempty"`
	Form           map[string]interface{} `json:"form,omitempty"`
	Selection      interface{}            `json:"selection,omitempty"`
	Collection     interface{}            `json:"collection,omitempty"`
	CollectionInfo map[string]interface{} `json:"collectionInfo,omitempty"`
	Metrics        interface{}            `json:"metrics,omitempty"`
	FormStatus     map[string]interface{} `json:"formStatus,omitempty"`
}

type ClientSnapshot struct {
	ClientID   string
	Namespace  string
	Snapshot   *Snapshot
	UpdatedAt  time.Time
	LastPollAt time.Time
	Transport  string
}

func (r *Registry) snapshots() ([]ClientSnapshot, error) {
	if r == nil || r.bridge == nil {
		return nil, fmt.Errorf("ui bridge not configured")
	}
	entries := r.bridge.Hub().SnapshotEntries()
	result := make([]ClientSnapshot, 0, len(entries))
	for _, entry := range entries {
		raw := entry.Snapshot
		var snap Snapshot
		if err := json.Unmarshal(raw, &snap); err != nil {
			continue
		}
		if r.state != nil {
			r.state.ingestSnapshot(entry.Namespace, entry.ClientID, &snap, raw, entry.UpdatedAt)
		}
		if strings.TrimSpace(snap.ClientID) == "" {
			snap.ClientID = strings.TrimSpace(entry.ClientID)
		}
		result = append(result, ClientSnapshot{
			ClientID:   strings.TrimSpace(entry.ClientID),
			Namespace:  strings.TrimSpace(entry.Namespace),
			Snapshot:   &snap,
			UpdatedAt:  entry.UpdatedAt,
			LastPollAt: entry.LastPollAt,
			Transport:  strings.TrimSpace(entry.Transport),
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].LastPollAt.After(result[j].LastPollAt) {
			return true
		}
		if result[j].LastPollAt.After(result[i].LastPollAt) {
			return false
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

func (r *Registry) RecordEvent(ns, clientID string, event UIEvent) {
	if r == nil || r.state == nil {
		return
	}
	r.state.recordEvent(ns, clientID, event)
}

// RecordConversationEvent stores browser lifecycle events independently of the
// transient live-window snapshot. This keeps recent report context available
// while a client reconnects or refreshes its UI bridge registration.
func (r *Registry) RecordConversationEvent(conversationID string, event UIEvent) UIEvent {
	if r == nil || r.state == nil {
		return event
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return event
	}
	event.ConversationID = conversationID
	return r.state.recordEvent("conversation", conversationID, event)
}

func (r *Registry) ListConversationEvents(conversationID string) []UIEvent {
	if r == nil || r.state == nil {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}
	return r.state.listEvents("conversation", conversationID)
}

// FindAuthorizedConversationWindow resolves an exact window identity previously
// established by a trusted UI command, snapshot, or accepted browser event.
func (r *Registry) FindAuthorizedConversationWindow(conversationID, clientID, windowID, windowKey string) (UIEvent, bool) {
	if r == nil || r.state == nil {
		return UIEvent{}, false
	}
	return r.state.findAuthorizedWindow(conversationID, clientID, windowID, windowKey)
}

func (r *Registry) ListEvents(conversationID, clientID, windowID, windowKey string, limit int, sinceSeq int64) []UIEvent {
	if r == nil || r.state == nil {
		return nil
	}
	clientID = strings.TrimSpace(clientID)
	windowID = strings.TrimSpace(windowID)
	windowKey = strings.TrimSpace(windowKey)
	conversationID = strings.TrimSpace(conversationID)
	if limit <= 0 {
		limit = 10
	}
	items, err := r.ListReadableByConversation(context.Background(), conversationID)
	if err != nil {
		return nil
	}
	var out []UIEvent
	for _, event := range r.ListConversationEvents(conversationID) {
		if sinceSeq > 0 && event.Seq <= sinceSeq {
			continue
		}
		if clientID != "" && strings.TrimSpace(event.ClientID) != clientID {
			continue
		}
		if windowID != "" && strings.TrimSpace(event.WindowID) != windowID {
			continue
		}
		if windowID == "" && windowKey != "" && strings.TrimSpace(event.WindowKey) != windowKey {
			continue
		}
		out = append(out, event)
	}
	for _, item := range items {
		if clientID != "" && strings.TrimSpace(item.ClientID) != clientID {
			continue
		}
		events := r.state.listEvents(item.Namespace, item.ClientID)
		for _, event := range events {
			if sinceSeq > 0 && event.Seq <= sinceSeq {
				continue
			}
			if conversationID != "" && strings.TrimSpace(event.ConversationID) != conversationID {
				continue
			}
			if windowID != "" && strings.TrimSpace(event.WindowID) != windowID {
				continue
			}
			if windowID == "" && windowKey != "" && strings.TrimSpace(event.WindowKey) != windowKey {
				continue
			}
			out = append(out, event)
		}
	}
	if len(out) == 0 && clientID != "" {
		if windowID != "" {
			for _, item := range items {
				if strings.TrimSpace(item.ClientID) == clientID || item.Snapshot == nil {
					continue
				}
				if !snapshotHasWindowID(item.Snapshot, conversationID, windowID) {
					continue
				}
				for _, event := range r.state.listEvents(item.Namespace, item.ClientID) {
					if sinceSeq > 0 && event.Seq <= sinceSeq {
						continue
					}
					if conversationID != "" && strings.TrimSpace(event.ConversationID) != conversationID {
						continue
					}
					if strings.TrimSpace(event.WindowID) != windowID {
						continue
					}
					out = append(out, event)
				}
			}
		}
	}
	if len(out) == 0 && clientID != "" {
		for _, event := range r.state.listEvents("", clientID) {
			if sinceSeq > 0 && event.Seq <= sinceSeq {
				continue
			}
			if conversationID != "" && strings.TrimSpace(event.ConversationID) != conversationID {
				continue
			}
			if windowID != "" && strings.TrimSpace(event.WindowID) != windowID {
				continue
			}
			if windowID == "" && windowKey != "" && strings.TrimSpace(event.WindowKey) != windowKey {
				continue
			}
			out = append(out, event)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Seq < out[j].Seq
	})
	if len(out) > limit {
		out = append([]UIEvent(nil), out[len(out)-limit:]...)
	}
	return out
}

func snapshotHasWindowID(snap *Snapshot, conversationID, windowID string) bool {
	filtered := filterSnapshotForConversation(snap, conversationID)
	if filtered == nil {
		return false
	}
	for i := range filtered.Windows {
		if strings.TrimSpace(filtered.Windows[i].WindowID) == windowID {
			return true
		}
	}
	return false
}

func isFreshSnapshot(item ClientSnapshot, now time.Time) bool {
	if item.Snapshot == nil {
		return false
	}
	if item.UpdatedAt.IsZero() {
		return false
	}
	return now.Sub(item.UpdatedAt) <= defaultSnapshotFreshness
}

func isServiceableClient(item ClientSnapshot, now time.Time) bool {
	if strings.EqualFold(strings.TrimSpace(item.Transport), "ws") {
		return true
	}
	if item.LastPollAt.IsZero() {
		return false
	}
	return now.Sub(item.LastPollAt) <= defaultPollFreshness
}

func isReadableClient(item ClientSnapshot, now time.Time) bool {
	_ = now
	return item.Snapshot != nil && !item.UpdatedAt.IsZero()
}

func isAttachedClient(item ClientSnapshot, now time.Time) bool {
	if item.Snapshot == nil {
		return false
	}
	// Snapshot publication is content-addressed: an unchanged visible UI does
	// not continuously republish its snapshot. A current transport heartbeat is
	// therefore equally strong evidence that the client remains attached.
	return isFreshSnapshot(item, now) || isServiceableClient(item, now)
}

func isMainChatWindow(win WindowSnapshot) bool {
	return strings.TrimSpace(win.WindowID) == "chat/new" || strings.TrimSpace(win.WindowKey) == "chat/new"
}

func windowVisibleToConversation(win WindowSnapshot, conversationID string) bool {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return true
	}
	if isMainChatWindow(win) {
		return true
	}
	if strings.TrimSpace(win.ConversationID) == conversationID {
		return true
	}
	if strings.TrimSpace(win.Presentation) != "hosted" {
		return true
	}
	return false
}

func snapshotBelongsToConversation(snapshot *Snapshot, conversationID string) bool {
	if snapshot == nil {
		return false
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return true
	}
	if strings.TrimSpace(snapshot.ConversationID) == conversationID {
		return true
	}
	for _, win := range snapshot.Windows {
		if strings.TrimSpace(win.ConversationID) == conversationID {
			return true
		}
	}
	return false
}

func filterSnapshotForConversation(snapshot *Snapshot, conversationID string) *Snapshot {
	if snapshot == nil {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return snapshot
	}
	filtered := make([]WindowSnapshot, 0, len(snapshot.Windows))
	for _, win := range snapshot.Windows {
		if windowVisibleToConversation(win, conversationID) {
			filtered = append(filtered, win)
		}
	}
	selected := snapshot.Selected
	if strings.TrimSpace(selected.WindowID) != "" {
		matched := false
		for _, win := range filtered {
			if strings.TrimSpace(win.WindowID) == strings.TrimSpace(selected.WindowID) {
				matched = true
				break
			}
		}
		if !matched {
			selected.WindowID = ""
		}
	}
	return &Snapshot{
		ConversationID: snapshot.ConversationID,
		ClientID:       snapshot.ClientID,
		Selected:       selected,
		Windows:        filtered,
	}
}

func (r *Registry) ListByConversation(ctx context.Context, conversationID string) ([]ClientSnapshot, error) {
	return r.listByConversation(ctx, conversationID, true)
}

// ListAttachedByConversation returns clients with either a fresh snapshot or
// a current transport heartbeat. Commands can be queued after a fresh
// snapshot between polls, while unchanged clients remain attached through
// their heartbeat without having to republish identical state.
func (r *Registry) ListAttachedByConversation(ctx context.Context, conversationID string) ([]ClientSnapshot, error) {
	items, err := r.listByConversation(ctx, conversationID, false)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	result := make([]ClientSnapshot, 0, len(items))
	for _, item := range items {
		if isAttachedClient(item, now) {
			result = append(result, item)
		}
	}
	return result, nil
}

func (r *Registry) ListReadableByConversation(ctx context.Context, conversationID string) ([]ClientSnapshot, error) {
	return r.listByConversation(ctx, conversationID, false)
}

func (r *Registry) listByConversation(ctx context.Context, conversationID string, requireServiceable bool) ([]ClientSnapshot, error) {
	_ = ctx
	conversationID = strings.TrimSpace(conversationID)
	items, err := r.snapshots()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if conversationID == "" {
		result := make([]ClientSnapshot, 0, len(items))
		for _, item := range items {
			if requireServiceable {
				if isFreshSnapshot(item, now) && isServiceableClient(item, now) {
					result = append(result, item)
				}
				continue
			}
			if isReadableClient(item, now) {
				result = append(result, item)
			}
		}
		return result, nil
	}
	result := make([]ClientSnapshot, 0, len(items))
	for _, item := range items {
		if requireServiceable {
			if !isFreshSnapshot(item, now) {
				continue
			}
			if !isServiceableClient(item, now) {
				continue
			}
		} else if !isReadableClient(item, now) {
			continue
		}
		if snapshotBelongsToConversation(item.Snapshot, conversationID) {
			filteredSnapshot := filterSnapshotForConversation(item.Snapshot, conversationID)
			if filteredSnapshot == nil {
				continue
			}
			item.Snapshot = filteredSnapshot
			result = append(result, item)
		}
	}
	return result, nil
}

func (r *Registry) FindClient(ctx context.Context, clientID string) (*ClientSnapshot, error) {
	_ = ctx
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, fmt.Errorf("clientId is required")
	}
	items, err := r.snapshots()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, item := range items {
		if !isFreshSnapshot(item, now) {
			continue
		}
		if !isServiceableClient(item, now) {
			continue
		}
		if item.ClientID == clientID {
			copyItem := item
			return &copyItem, nil
		}
	}
	return nil, fmt.Errorf("client not found")
}

func (r *Registry) FindWindow(ctx context.Context, conversationID, clientID, windowID, windowKey string) (string, string, *Snapshot, *WindowSnapshot, error) {
	return r.findWindow(ctx, conversationID, clientID, windowID, windowKey, true)
}

func (r *Registry) FindReadableWindow(ctx context.Context, conversationID, clientID, windowID, windowKey string) (string, string, *Snapshot, *WindowSnapshot, error) {
	return r.findWindow(ctx, conversationID, clientID, windowID, windowKey, false)
}

func (r *Registry) findWindow(ctx context.Context, conversationID, clientID, windowID, windowKey string, requireServiceable bool) (string, string, *Snapshot, *WindowSnapshot, error) {
	windowID = strings.TrimSpace(windowID)
	windowKey = strings.TrimSpace(windowKey)
	if windowID == "" && windowKey == "" {
		return "", "", nil, nil, fmt.Errorf("windowId or windowKey is required")
	}
	items, err := r.listByConversation(ctx, conversationID, requireServiceable)
	if err != nil {
		return "", "", nil, nil, err
	}
	allItems := items
	preferredClientID := strings.TrimSpace(clientID)
	if preferredClientID != "" {
		filtered := make([]ClientSnapshot, 0, len(items))
		for _, item := range items {
			if item.ClientID == preferredClientID {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if clientID, namespace, snap, win, ok := findWindowInClientSnapshots(items, conversationID, windowID, windowKey); ok {
		return clientID, namespace, snap, win, nil
	}
	if preferredClientID != "" && windowID != "" {
		if clientID, namespace, snap, win, ok := findWindowInClientSnapshots(allItems, conversationID, windowID, ""); ok {
			return clientID, namespace, snap, win, nil
		}
	}
	if windowID != "" && r.state != nil {
		if eventClientID, eventNamespace, event, ok := r.state.findRecentWindowEvent(conversationID, preferredClientID, windowID, defaultWindowEventFreshness); ok {
			return eventClientID, eventNamespace, nil, &WindowSnapshot{
				WindowID:       strings.TrimSpace(event.WindowID),
				WindowKey:      firstNonEmpty(strings.TrimSpace(event.WindowKey), windowKey),
				ConversationID: strings.TrimSpace(event.ConversationID),
			}, nil
		}
	}
	return "", "", nil, nil, fmt.Errorf("window not found")
}

func findWindowInClientSnapshots(items []ClientSnapshot, conversationID, windowID, windowKey string) (string, string, *Snapshot, *WindowSnapshot, bool) {
	for _, item := range items {
		if item.Snapshot == nil {
			continue
		}
		filteredSnapshot := filterSnapshotForConversation(item.Snapshot, conversationID)
		if filteredSnapshot == nil {
			continue
		}
		for i := range filteredSnapshot.Windows {
			win := &filteredSnapshot.Windows[i]
			if windowID != "" && strings.TrimSpace(win.WindowID) == windowID {
				return item.ClientID, item.Namespace, filteredSnapshot, win, true
			}
			if windowID == "" && windowKey != "" && strings.TrimSpace(win.WindowKey) == windowKey {
				return item.ClientID, item.Namespace, filteredSnapshot, win, true
			}
		}
	}
	return "", "", nil, nil, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
