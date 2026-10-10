package datasource

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
)

// Bound resource cache identity excludes lease expiry: admission/final guards
// still verify the original lease, while cached rows keep their original TTL.
func boundResourceCacheKey(key string, pin identity.ResolvedResource, target *types.WindowTarget, descriptor *dsproto.DataSource, principal Identity) (string, error) {
	selected := types.WindowTarget{}
	if target != nil {
		selected.Platform = target.Platform
		selected.FormFactor = target.FormFactor
		selected.Surface = target.Surface
	}
	if principal.User == "" {
		return "", identity.ErrResourceDenied
	}
	principal.Conversation = ""

	identityValue := struct {
		Principal  Identity
		URI        string
		Provider   string
		Candidate  identity.ResourceCandidate
		Authority  string
		Target     types.WindowTarget
		Descriptor *dsproto.DataSource
	}{principal, pin.URI, pin.ProviderIdentity, pin.ResourceCandidate, pin.AuthorityBinding, selected, descriptor}
	raw, err := json.Marshal(identityValue)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return key + "|resource:" + hex.EncodeToString(sum[:]), nil
}
