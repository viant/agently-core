package resource

import (
	"context"
	identity "github.com/viant/agently-core/protocol/resource"
	"time"
)

// PublicReadActors carries no authenticated identity or business permissions.
// Managing providers still decide whether each public list/get is available.
func PublicReadActors() (ActorResolver, ActorVerifier) {
	actor := func(ctx context.Context) (identity.VerifiedActor, error) {
		if ctx == nil || ctx.Err() != nil {
			return identity.VerifiedActor{}, identity.ErrResourceDenied
		}
		return identity.VerifiedActor{PublicRead: true, ValidUntil: time.Now().Add(time.Minute)}, nil
	}
	verify := func(ctx context.Context, held identity.VerifiedActor) error {
		if ctx == nil || ctx.Err() != nil || !held.PublicRead || !held.ReadValid(time.Now()) {
			return identity.ErrResourceDenied
		}
		return nil
	}
	return actor, verify
}
