package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

type Registry struct {
	bridge *forgeuisvc.Service
}

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
	WindowID    string                        `json:"windowId,omitempty"`
	WindowKey   string                        `json:"windowKey,omitempty"`
	WindowTitle string                        `json:"windowTitle,omitempty"`
	Parameters  map[string]interface{}        `json:"parameters,omitempty"`
	InTab       bool                          `json:"inTab,omitempty"`
	IsModal     bool                          `json:"isModal,omitempty"`
	IsMinimized bool                          `json:"isMinimized,omitempty"`
	DataSources map[string]DataSourceSnapshot `json:"dataSources,omitempty"`
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
	ClientID string
	Snapshot *Snapshot
}

func (r *Registry) snapshots() ([]ClientSnapshot, error) {
	if r == nil || r.bridge == nil {
		return nil, fmt.Errorf("ui bridge not configured")
	}
	clients := r.bridge.Hub().ListClients("default")
	result := make([]ClientSnapshot, 0, len(clients))
	for _, clientID := range clients {
		raw := r.bridge.Hub().Snapshot("default", clientID)
		if len(raw) == 0 {
			continue
		}
		var snap Snapshot
		if err := json.Unmarshal(raw, &snap); err != nil {
			continue
		}
		if strings.TrimSpace(snap.ClientID) == "" {
			snap.ClientID = strings.TrimSpace(clientID)
		}
		result = append(result, ClientSnapshot{
			ClientID: strings.TrimSpace(clientID),
			Snapshot: &snap,
		})
	}
	return result, nil
}

func (r *Registry) ListByConversation(ctx context.Context, conversationID string) ([]ClientSnapshot, error) {
	_ = ctx
	conversationID = strings.TrimSpace(conversationID)
	items, err := r.snapshots()
	if err != nil {
		return nil, err
	}
	if conversationID == "" {
		return items, nil
	}
	result := make([]ClientSnapshot, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Snapshot.ConversationID) == conversationID {
			result = append(result, item)
		}
	}
	return result, nil
}

func (r *Registry) FindWindow(ctx context.Context, conversationID, clientID, windowID, windowKey string) (string, *Snapshot, *WindowSnapshot, error) {
	windowID = strings.TrimSpace(windowID)
	windowKey = strings.TrimSpace(windowKey)
	if windowID == "" && windowKey == "" {
		return "", nil, nil, fmt.Errorf("windowId or windowKey is required")
	}
	items, err := r.ListByConversation(ctx, conversationID)
	if err != nil {
		return "", nil, nil, err
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
		for i := range item.Snapshot.Windows {
			win := &item.Snapshot.Windows[i]
			if windowID != "" && strings.TrimSpace(win.WindowID) == windowID {
				return item.ClientID, item.Snapshot, win, nil
			}
			if windowID == "" && windowKey != "" && strings.TrimSpace(win.WindowKey) == windowKey {
				return item.ClientID, item.Snapshot, win, nil
			}
		}
	}
	return "", nil, nil, fmt.Errorf("window not found")
}
