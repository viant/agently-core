package window

import "github.com/viant/forge/backend/types"

// PresentationInput carries authored UI rendering hints, never access grants.
type PresentationInput struct {
	WindowID         string
	ConversationID   string
	Window           *types.Window
	WindowForm       map[string]any
	ResourceData     map[string]any
	ResourceType     string
	ResourceID       int
	ResourceIDString string
	SchemaVersion    int
}
