package resource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/viant/agently-core/service/policy"
	identity "github.com/viant/agently-core/protocol/resource"
	"time"
)

// AuthorityActors explicitly adapts a trusted fresh authority callback. Full
// facts participate in the partition, including roles, exposures and context
// revisions; a verified subject alone is not a cache identity.
func AuthorityActors(resolve policy.AuthoritySnapshotResolver) (ActorResolver, ActorVerifier) {
	actor := func(ctx context.Context) (identity.VerifiedActor, error) {
		if resolve == nil || ctx == nil || ctx.Err() != nil {
			return identity.VerifiedActor{}, identity.ErrResourceDenied
		}
		p, e := resolve(ctx)
		if e != nil {
			return identity.VerifiedActor{}, e
		}
		facts := p.Facts
		facts.ValidUntil = time.Time{}
		material := struct {
			Facts             any
			Entities          any
			Account, Revision string
		}{facts, facts.Entities, p.AccountID, p.IdentityRevision}
		raw, e := json.Marshal(material)
		if e != nil {
			return identity.VerifiedActor{}, e
		}
		sum := sha256.Sum256(raw)
		a := identity.VerifiedActor{Subject: p.Facts.Subject, Issuer: p.Facts.Issuer, TenantID: p.Facts.Tenant, AccountID: p.AccountID, IdentityRevision: hex.EncodeToString(sum[:]), ValidUntil: p.Facts.ValidUntil}
		if !a.Valid(time.Now()) || ctx.Err() != nil {
			return identity.VerifiedActor{}, identity.ErrResourceDenied
		}
		return a, nil
	}
	verify := func(ctx context.Context, expected identity.VerifiedActor) error {
		fresh, e := actor(ctx)
		if e != nil {
			return e
		}
		if fresh.Subject != expected.Subject || fresh.Issuer != expected.Issuer || fresh.TenantID != expected.TenantID || fresh.AccountID != expected.AccountID || fresh.IdentityRevision != expected.IdentityRevision || !expected.Valid(time.Now()) {
			return identity.ErrResourceDenied
		}
		return nil
	}
	return actor, verify
}
