package permittedview

import (
	"context"
	"time"

	forgetypes "github.com/viant/forge/backend/types"
)

type Request struct {
	ResourceType                string   `json:"resourceType"`
	ResourceIDs                 []int    `json:"resourceIds,omitempty"`
	RequestedCapabilities       []string `json:"requestedCapabilities,omitempty"`
	RequestedGlobalCapabilities []string `json:"requestedGlobalCapabilities,omitempty"`
	IncludePrincipal            bool     `json:"includePrincipal,omitempty"`
}

type Resolver interface {
	Resolve(context.Context, *Request) (*Snapshot, error)
}

type Snapshot struct {
	AuthorizationVersion string               `json:"authorizationVersion"`
	ExpiresAt            time.Time            `json:"expiresAt"`
	Principal            map[string]any       `json:"principal,omitempty"`
	Account              map[string]any       `json:"account,omitempty"`
	GlobalCapabilities   map[string]bool      `json:"globalCapabilities,omitempty"`
	Resources            map[string]*Resource `json:"resources,omitempty"`
}

type Resource struct {
	Type         string          `json:"type"`
	ID           int             `json:"id"`
	Roles        []string        `json:"roles,omitempty"`
	Capabilities map[string]bool `json:"capabilities"`
}

type BoundView struct {
	WindowID       string
	ConversationID string
	Window         *forgetypes.Window
	WindowForm     map[string]any
	ResourceData   map[string]any
	ResourceType   string
	ResourceID     int
}

type Result struct {
	Window         *forgetypes.Window
	Authorization  *Snapshot
	Resource       *Resource
	DataSourceRefs map[string]bool
	ExpiresAt      time.Time
	Denied         bool
	Diagnostics    []Diagnostic
}

type Diagnostic struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}
