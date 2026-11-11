package workspace

import (
	"context"
	"encoding/json"
	shared "github.com/viant/forge/backend/resourcehost"
)

// LayoutMenuRead contains already authorized visible applications and a final
// exact-pin check. Revision identifies the composed delivery, not source bytes.
type LayoutGlobalStyle struct {
	ResourceURI        string `json:"resourceUri"`
	ResourceConnection string `json:"resourceConnection"`
	ProviderIdentity   string `json:"providerIdentity"`
	StyleRevision      string `json:"styleRevision"`
}
type LayoutMenuRead struct {
	StyleDiagnostics []string

	GlobalStyle          *LayoutGlobalStyle
	PreserveApplications bool
	Applications         json.RawMessage
	Revision             string
	Revalidate           func(context.Context) error
	Close                func() error
	// ProviderOwnedApplications have already been selected by their managing
	// MCP. The host applies presentation conditions, not another window inventory.
	ProviderOwnedApplications bool
}
type LayoutMenuReader func(context.Context) (*LayoutMenuRead, error)

// SetLayoutMenuReader explicitly enables bounded menu delivery. A configured
// denial never falls back to the unfiltered layout applications.
func (h *MetadataHandler) SetLayoutMenuReader(reader LayoutMenuReader) {
	if h != nil {
		h.layoutMenuReader = reader
	}
}

// The selector matches configured source ownership; it is never an authority
// claim or a browser-selected connection. Other applications receive no values.
type layoutMenuParametersKey struct{}

func layoutMenuReadContext(ctx context.Context, owner, application string) context.Context {
	input, _ := ctx.Value(layoutMenuParametersKey{}).(LayoutRequest)
	var parameters map[string]any
	if application != "" && input.ApplicationID == application && input.ProviderIdentity == owner {
		parameters = input.Parameters
	}
	return shared.WithMenuParameters(ctx, parameters)
}
