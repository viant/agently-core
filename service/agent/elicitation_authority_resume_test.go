package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	registry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	toolservice "github.com/viant/agently-core/protocol/tool/service"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/policy"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
)

// Exercise the admitted continuation context through the existing host registry
// guard. Detaching the HTTP request must retain the trusted facts/pin leases.
func TestPreparedElicitationContinuationRetainsOriginalAuthorityAndPinExpiry(t *testing.T) {
	for _, state := range []string{"live", "authority_expired", "pin_expired"} {
		t.Run(state, func(t *testing.T) {
			svc, input, _, model := continuationFixture(t)
			lease := time.Now().Add(time.Minute)
			principal := gating.Principal{Facts: authz.Facts{Subject: "user", Issuer: "issuer", Tenant: "team", ValidUntil: lease}, AccountID: "account", IdentityRevision: "original"}
			authority, originalLease, err := policy.ResolveAuthorityBinding(context.Background(), func(context.Context) (gating.Principal, error) { return principal, nil })
			require.NoError(t, err)
			require.Equal(t, lease, originalLease)
			pin := identity.ResolvedResource{ProviderIdentity: "provider-a", URI: "datasource://team/accounts", ResourceCandidate: identity.ResourceCandidate{Kind: identity.StampedCandidate, Revision: "release-1", ContentFingerprint: strings.Repeat("a", 64)}, AuthorityBinding: authority, ValidUntil: originalLease}
			if state == "authority_expired" {
				principal.Facts.ValidUntil = time.Now().Add(-time.Second)
			}
			if state == "pin_expired" {
				pin.ValidUntil = time.Now().Add(-time.Second)
			}
			mapper, err := policy.NewStaticBackendMapper([]policy.BackendBinding{{Operation: "tool.execute", ID: "resume/fixture:read", UseResolvedResource: true, Resource: authz.Resource{Tenant: "team"}, Action: "read"}})
			require.NoError(t, err)
			mgr, err := manager.New(nil)
			require.NoError(t, err)
			reg, err := registry.NewWithManager(mgr)
			require.NoError(t, err)
			provider := &continuationAuthorityProvider{}
			require.NoError(t, reg.AddInternalService(provider))
			reg.SetAuthorizationGuard(func(ctx context.Context, name string, inputs map[string]interface{}) error {
				observed, ok := requestctx.ResolvedResourceFromContext(ctx)
				require.True(t, ok)
				require.Equal(t, pin, *observed, "continuation must preserve the original resolved pin")
				currentAuthority, currentLease, err := policy.ResolveAuthorityBinding(ctx, func(context.Context) (gating.Principal, error) { return principal, nil })
				if err != nil {
					return err
				}
				require.Equal(t, authority, currentAuthority)
				require.Equal(t, originalLease, currentLease, "detaching the request must not extend the authority lease")
				_, _, _, _, err = mapper(ctx, "tool.execute", name, inputs)
				return err
			})
			requestCtx, cancel := context.WithCancel(requestctx.WithResolvedResource(context.Background(), pin))
			cancel() // The request is gone; neither trusted lease changes.
			execute, err := svc.prepareContinuation(context.WithoutCancel(requestCtx), input, "resume-turn", 3, &QueryOutput{}, func(ctx context.Context) error {
				_, err := reg.Execute(ctx, "resume/fixture:read", map[string]interface{}{})
				return err
			})
			require.NoError(t, err)
			err = execute()
			if state == "live" {
				require.NoError(t, err)
				require.Equal(t, 1, provider.calls)
				require.EqualValues(t, 1, model.calls.Load())
			} else {
				require.ErrorIs(t, err, policy.ErrDenied)
				require.Zero(t, provider.calls, "expired authority or pin must block provider dispatch")
				require.EqualValues(t, 0, model.calls.Load())
			}
		})
	}
}

type continuationAuthorityProvider struct{ calls int }

func (*continuationAuthorityProvider) Name() string { return "resume/fixture" }
func (*continuationAuthorityProvider) Methods() toolservice.Signatures {
	return toolservice.Signatures{{Name: "read", Input: reflect.TypeOf(&struct{}{}), Output: reflect.TypeOf(&map[string]string{})}}
}
func (s *continuationAuthorityProvider) Method(string) (toolservice.Executable, error) {
	return func(_ context.Context, _, output interface{}) error {
		s.calls++
		*output.(*map[string]string) = map[string]string{"status": "read"}
		return nil
	}, nil
}
