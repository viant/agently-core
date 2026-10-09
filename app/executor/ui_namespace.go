package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/viant/agently-core/service/policy"
	ui "github.com/viant/agently-core/service/primitiveprovider"
	"strings"
	"time"
)

// UI identity is server verified. Roles, revisions, rolling leases and client
// token hints must not change its namespace across refresh or authorization work.
func trustedUINamespace(snapshot policy.AuthoritySnapshotResolver, clearScopes ...func(context.Context) context.Context) ui.NamespaceResolver {
	return func(ctx context.Context) (ui.NamespaceIdentity, error) {
		if snapshot == nil || ctx == nil || ctx.Err() != nil {
			return ui.NamespaceIdentity{}, policy.ErrIdentityRejected
		}
		if len(clearScopes) > 0 && clearScopes[0] != nil {
			ctx = clearScopes[0](ctx)
			if ctx == nil || ctx.Err() != nil {
				return ui.NamespaceIdentity{}, policy.ErrIdentityRejected
			}
		}
		principal, err := snapshot(ctx)
		if err != nil || ctx.Err() != nil || !principal.Facts.ValidUntil.After(time.Now()) {
			return ui.NamespaceIdentity{}, policy.ErrIdentityRejected
		}
		keys := []string{principal.Facts.Issuer, principal.Facts.Tenant, principal.AccountID, principal.Facts.Subject}
		for _, key := range keys {
			if strings.TrimSpace(key) == "" {
				return ui.NamespaceIdentity{}, policy.ErrIdentityRejected
			}
		}
		raw, _ := json.Marshal(keys)
		sum := sha256.Sum256(raw)
		return ui.NamespaceIdentity{Namespace: "identity-" + hex.EncodeToString(sum[:]), ValidUntil: principal.Facts.ValidUntil}, nil
	}
}
