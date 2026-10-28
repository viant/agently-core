package cohort

import (
	"context"
	"time"

	resources "github.com/viant/forge/backend/resourcehost"
	forgetypes "github.com/viant/forge/backend/types"
)

// WindowSource is the published-window contract consumed by a host adapter.
type WindowSource interface {
	OpenWindowVariant(context.Context, string, *forgetypes.WindowTarget) (*forgetypes.WindowResourceVariant, string, error)
}

// WindowSourceFunc connects an owner-bound shared runtime loader to host UI.
type WindowSourceFunc func(context.Context, string, *forgetypes.WindowTarget) (*forgetypes.WindowResourceVariant, string, error)

func (f WindowSourceFunc) OpenWindowVariant(ctx context.Context, uri string, target *forgetypes.WindowTarget) (*forgetypes.WindowResourceVariant, string, error) {
	return f(ctx, uri, target)
}

// Source owns synchronization and producer-issued admission for a replica.
type Source interface {
	WindowSource
	Sync(context.Context) error
	EnsureAdmitted(context.Context) error
}

// Binding is an operator-owned source and its background lifecycle.
type Binding struct {
	ConnectionName    string
	ConnectionAliases []string
	ProviderIdentity  string
	Namespaces        []string // configured claims, independent of current grants
	Source            Source
	Metadata          resources.PublishedMetadataSource
	PollInterval      time.Duration // fallback when notification Start is absent
	Start             func(context.Context) error
	Close             func()
}

// WindowSource preserves producer admission before reading the local replica.
func (b Binding) WindowSource() WindowSource {
	return admittingSource{source: b.Source}
}

type admittingSource struct{ source Source }

func (s admittingSource) OpenWindowVariant(ctx context.Context, uri string, target *forgetypes.WindowTarget) (*forgetypes.WindowResourceVariant, string, error) {
	if err := s.source.EnsureAdmitted(ctx); err != nil {
		return nil, "", err
	}
	return s.source.OpenWindowVariant(ctx, uri, target)
}
