// Package workspace defines conversation-owned UI descriptors shared by renderers.
package workspace

import (
	"context"
	"strings"
	"time"

	"github.com/viant/agently-core/runtime/requestctx"
)

type Origin struct {
	MessageID  string `json:"messageId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	TurnID     string `json:"turnId,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
}

type Placement struct {
	InitialWorkspaceMode string   `json:"initialWorkspaceMode,omitempty"`
	Preferred            string   `json:"preferred"`
	Allowed              []string `json:"allowed"`
}

type Lifecycle struct {
	CreatedAt     string `json:"createdAt,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
	State         string `json:"state"`
	RestorePolicy string `json:"restorePolicy"`
	RefreshPolicy string `json:"refreshPolicy"`
}

type Content struct {
	WindowID   string                 `json:"windowId,omitempty"`
	Renderer   string                 `json:"renderer"`
	WindowKey  string                 `json:"windowKey,omitempty"`
	Parameters map[string]interface{} `json:"parameters,omitempty"`
}

type Object struct {
	LastActivatedBy Origin            `json:"lastActivatedBy,omitempty"`
	Content         Content           `json:"content"`
	Navigation      map[string]string `json:"navigation,omitempty"`
	Capabilities    map[string]bool   `json:"capabilities,omitempty"`
	StateRef        string            `json:"stateRef,omitempty"`
	Revision        int               `json:"revision"`
	Version         int               `json:"version"`
	ObjectID        string            `json:"objectId"`
	ConversationID  string            `json:"conversationId"`
	Kind            string            `json:"kind"`
	Origin          Origin            `json:"origin"`
	Placement       Placement         `json:"placement"`
	Lifecycle       Lifecycle         `json:"lifecycle"`
}

// New constructs a pending descriptor. Only an acknowledged open may mark it ready.
// Origin comes from execution context, never resource parameters or assistant prose.
func New(ctx context.Context, windowID, conversationID string) *Object {
	meta, _ := requestctx.TurnMetaFromContext(ctx)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return &Object{
		Content: Content{Renderer: "forgeWindow"}, Revision: 1,
		StateRef:     "workspace-state:" + conversationID + ":" + strings.TrimSpace(windowID),
		Capabilities: map[string]bool{"close": true, "focus": true, "split": true},
		Version:      1, ObjectID: "workspace:" + strings.TrimSpace(windowID),
		ConversationID: conversationID, Kind: "resource",
		LastActivatedBy: Origin{ToolName: "ui/view/open", TurnID: meta.TurnID, ToolCallID: requestctx.ToolMessageIDFromContext(ctx)},
		Origin:          Origin{ToolName: "ui/view/open", TurnID: meta.TurnID, ToolCallID: requestctx.ToolMessageIDFromContext(ctx)},
		Placement:       Placement{InitialWorkspaceMode: "focus", Preferred: "workspace", Allowed: []string{"workspace"}},
		Lifecycle:       Lifecycle{CreatedAt: now, UpdatedAt: now, State: "opening", RestorePolicy: "conversation", RefreshPolicy: "explicit"},
	}
}
