package token

import (
	"context"
	"errors"
	"fmt"
	"strings"

	scyauth "github.com/viant/scy/auth"
)

// ErrNoBrokerForProvider marks a refresh request routed to a provider without
// a registered broker. It is a routing/configuration condition — never
// invalid_grant — so callers must preserve the stored credential (cooldown,
// skip) and must never delete or modify the token row because of it.
var ErrNoBrokerForProvider = errors.New("token: no refresh broker registered for provider")

// IsNoBrokerForProvider reports whether err marks missing broker routing.
func IsNoBrokerForProvider(err error) bool {
	return errors.Is(err, ErrNoBrokerForProvider)
}

// BrokerRegistry resolves refresh brokers by token key provider (workspace
// provider name or delegated storage key).
type BrokerRegistry interface {
	Broker(ctx context.Context, provider string) (Broker, bool)
}

// WorkspaceAliasMatcher reports whether a provider value is the workspace
// identity provider or one of its trusted legacy aliases.
type WorkspaceAliasMatcher func(provider string) bool

// RoutingBroker implements Broker by routing Refresh/Exchange to the broker
// registered for token.Key.Provider. There is exactly one token.Manager: the
// router lets it serve every provider while keeping leases, singleflight,
// miss caches and retry caches shared.
//
// Routing rules:
//   - Workspace aliases go to the workspace broker.
//   - Other providers are resolved through the registry.
//   - Unknown providers fail with ErrNoBrokerForProvider; they are never sent
//     to the workspace broker and never treated as invalid_grant.
type RoutingBroker struct {
	Workspace        Broker
	Registry         BrokerRegistry
	IsWorkspaceAlias WorkspaceAliasMatcher
}

func (r *RoutingBroker) broker(ctx context.Context, provider string) (Broker, error) {
	provider = strings.TrimSpace(provider)
	if r.IsWorkspaceAlias != nil && r.IsWorkspaceAlias(provider) {
		if r.Workspace == nil {
			return nil, fmt.Errorf("%w: workspace provider %q", ErrNoBrokerForProvider, provider)
		}
		return r.Workspace, nil
	}
	if r.Registry != nil {
		if broker, ok := r.Registry.Broker(ctx, provider); ok && broker != nil {
			return broker, nil
		}
	}
	if r.IsWorkspaceAlias == nil && r.Workspace != nil && r.Registry == nil {
		// Degenerate configuration: no routing information at all preserves
		// the legacy single-broker behaviour.
		return r.Workspace, nil
	}
	return nil, fmt.Errorf("%w: provider %q", ErrNoBrokerForProvider, provider)
}

// Refresh routes the refresh to the provider's broker.
func (r *RoutingBroker) Refresh(ctx context.Context, key Key, refreshToken string) (*scyauth.Token, error) {
	broker, err := r.broker(ctx, key.Provider)
	if err != nil {
		return nil, err
	}
	return broker.Refresh(ctx, key, refreshToken)
}

// Exchange routes the code exchange to the provider's broker.
func (r *RoutingBroker) Exchange(ctx context.Context, key Key, code string) (*scyauth.Token, error) {
	broker, err := r.broker(ctx, key.Provider)
	if err != nil {
		return nil, err
	}
	return broker.Exchange(ctx, key, code)
}
