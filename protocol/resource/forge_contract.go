package resource

import (
	"context"
	"time"

	shared "github.com/viant/forge/backend/resource"
)

// These aliases keep existing Agently imports source-compatible while Forge
// owns the resource contract. No independent resource identity or revision
// implementation remains in this module.
type (
	VerifiedActor          = shared.VerifiedActor
	ResourceAuthority      = shared.ResourceAuthority
	ResourceAuthorityFunc  = shared.ResourceAuthorityFunc
	ResourceURI            = shared.ResourceURI
	ResourceRef            = shared.ResourceRef
	ResourceCandidate      = shared.ResourceCandidate
	ResolvedResource       = shared.ResolvedResource
	ResourceDecision       = shared.ResourceDecision
	ResourceSource         = shared.ResourceSource
	ResourceRevisionPolicy = shared.ResourceRevisionPolicy
	ResourceResolver       = shared.ResourceResolver
	LocalResource          = shared.LocalResource
	RenewalAuthorizer      = shared.RenewalAuthorizer
	WorkspaceListScope     = shared.WorkspaceListScope
	CanonicalActor         = shared.CanonicalActor
	SelectionBinding       = shared.SelectionBinding
	SelectionHMAC          = shared.SelectionHMAC
	PolicyID               = shared.PolicyID
)

const (
	WorkingCandidate      = shared.WorkingCandidate
	StampedCandidate      = shared.StampedCandidate
	ResourcePathMaxLength = shared.ResourcePathMaxLength
	PolicyVersion         = shared.PolicyVersion
)

var (
	ErrResource       = shared.ErrResource
	ErrResourceDenied = shared.ErrResourceDenied
	ErrResourceStale  = shared.ErrResourceStale
	ErrInvalid        = shared.ErrInvalid
)

func ParseResourceURI(value string) (ResourceURI, error) { return shared.ParseResourceURI(value) }
func ValidResourceKind(value string) bool                { return shared.ValidResourceKind(value) }
func SameResolvedResource(a, b ResolvedResource) bool    { return shared.SameResolvedResource(a, b) }
func ContentFingerprint(raw []byte) string               { return shared.ContentFingerprint(raw) }
func MetadataExpiryValid(until, now time.Time) bool      { return shared.MetadataExpiryValid(until, now) }
func MinimumMetadataExpiry(left, right time.Time) time.Time {
	return shared.MinimumMetadataExpiry(left, right)
}
func WithMetadataExpiry(ctx context.Context, until time.Time) (context.Context, context.CancelFunc) {
	return shared.WithMetadataExpiry(ctx, until)
}
func MetadataExpiryExtends(next, held time.Time) bool {
	return shared.MetadataExpiryExtends(next, held)
}
func NewSelectionHMAC(key []byte) (*SelectionHMAC, error) { return shared.NewSelectionHMAC(key) }
func SelectionParametersDigest(parameters map[string]any) (string, error) {
	return shared.SelectionParametersDigest(parameters)
}
func WorkspacePolicyID(tenantID, accountID, studioKind, workspaceID string) (PolicyID, error) {
	return shared.WorkspacePolicyID(tenantID, accountID, studioKind, workspaceID)
}
func ObjectPolicyID(tenantID, accountID, studioKind, workspaceID, kind, objectID string) (PolicyID, error) {
	return shared.ObjectPolicyID(tenantID, accountID, studioKind, workspaceID, kind, objectID)
}
