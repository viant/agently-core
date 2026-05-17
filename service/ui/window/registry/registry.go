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
}

const defaultSnapshotFreshness = 15 * time.Second

func New(bridge *forgeuisvc.Service) *Registry {
	return &Registry{bridge: bridge}
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
	WindowID       string                        `json:"windowId,omitempty"`
	WindowKey      string                        `json:"windowKey,omitempty"`
	WindowTitle    string                        `json:"windowTitle,omitempty"`
	ConversationID string                        `json:"conversationId,omitempty"`
	Presentation   string                        `json:"presentation,omitempty"`
	Region         string                        `json:"region,omitempty"`
	ParentKey      string                        `json:"parentKey,omitempty"`
	Parameters     map[string]interface{}        `json:"parameters,omitempty"`
	WindowForm     map[string]interface{}        `json:"windowForm,omitempty"`
	ViewState      map[string]interface{}        `json:"viewState,omitempty"`
	Metadata       map[string]interface{}        `json:"metadata,omitempty"`
	InTab          bool                          `json:"inTab,omitempty"`
	IsModal        bool                          `json:"isModal,omitempty"`
	IsMinimized    bool                          `json:"isMinimized,omitempty"`
	DataSources    map[string]DataSourceSnapshot `json:"dataSources,omitempty"`
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
	Metrics        map[string]interface{} `json:"metrics,omitempty"`
	FormStatus     map[string]interface{} `json:"formStatus,omitempty"`
}

type ClientSnapshot struct {
	ClientID  string
	Namespace string
	Snapshot  *Snapshot
	UpdatedAt time.Time
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
		if strings.TrimSpace(snap.ClientID) == "" {
			snap.ClientID = strings.TrimSpace(entry.ClientID)
		}
		result = append(result, ClientSnapshot{
			ClientID:  strings.TrimSpace(entry.ClientID),
			Namespace: strings.TrimSpace(entry.Namespace),
			Snapshot:  &snap,
			UpdatedAt: entry.UpdatedAt,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
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
			if isFreshSnapshot(item, now) {
				result = append(result, item)
			}
		}
		return result, nil
	}
	result := make([]ClientSnapshot, 0, len(items))
	for _, item := range items {
		if !isFreshSnapshot(item, now) {
			continue
		}
		if strings.TrimSpace(item.Snapshot.ConversationID) == conversationID {
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
		if item.ClientID == clientID {
			copyItem := item
			return &copyItem, nil
		}
	}
	return nil, fmt.Errorf("client not found")
}

func (r *Registry) FindWindow(ctx context.Context, conversationID, clientID, windowID, windowKey string) (string, string, *Snapshot, *WindowSnapshot, error) {
	windowID = strings.TrimSpace(windowID)
	windowKey = strings.TrimSpace(windowKey)
	if windowID == "" && windowKey == "" {
		return "", "", nil, nil, fmt.Errorf("windowId or windowKey is required")
	}
	items, err := r.ListByConversation(ctx, conversationID)
	if err != nil {
		return "", "", nil, nil, err
	}
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
				return item.ClientID, item.Namespace, filteredSnapshot, win, nil
			}
			if windowID == "" && windowKey != "" && strings.TrimSpace(win.WindowKey) == windowKey {
				return item.ClientID, item.Namespace, filteredSnapshot, win, nil
			}
		}
	}
	return "", "", nil, nil, fmt.Errorf("window not found")
}
