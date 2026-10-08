package resource

import "time"

// VerifiedActor is a narrow host projection of the agreed IdP principal for
// Studio mutations. The host must derive every field from verified identity.
type VerifiedActor struct {
	Subject          string
	Issuer           string
	TenantID         string
	AccountID        string
	IdentityRevision string
	ValidUntil       time.Time
}

func (a VerifiedActor) Valid(now time.Time) bool {
	return a.Subject != "" && a.Issuer != "" && internalID(a.TenantID) && internalID(a.AccountID) &&
		a.IdentityRevision != "" && a.ValidUntil.After(now)
}
