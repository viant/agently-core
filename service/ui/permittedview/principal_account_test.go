package permittedview

import (
	"context"
	"testing"
	"time"

	"github.com/viant/authz"
	"github.com/viant/authz/gating"
)

type presentationPrincipalProvider struct {
	calls   int
	account string
}

func (p *presentationPrincipalProvider) Resolve(context.Context) (authz.Facts, error) {
	panic("must use the same verified principal for facts and account")
}
func (p *presentationPrincipalProvider) ResolvePrincipal(context.Context) (gating.Principal, error) {
	p.calls++
	return gating.Principal{Facts: authz.Facts{Subject: "user", Issuer: "issuer", Tenant: "not-account", Roles: []string{"reader"}, Exposures: []string{"visible"}, ValidUntil: time.Now().Add(time.Minute)}, AccountID: p.account}, nil
}
func TestPrincipalPresentationIncludesVerifiedAccountWithoutResourceChecks(t *testing.T) {
	provider := &presentationPrincipalProvider{account: "17"}
	resolver := &AuthzResolver{Service: &authz.Service{Provider: provider}}
	request := &Request{IncludePrincipal: true}
	first, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || first.Account["id"] != "17" || len(first.Resources) != 0 || len(first.GlobalCapabilities) != 0 {
		t.Fatalf("unexpected snapshot: %+v calls=%d", first, provider.calls)
	}
	provider.account = "23"
	second, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Account["id"] != "23" || second.AuthorizationVersion == first.AuthorizationVersion {
		t.Fatal("account change must change presentation revision")
	}
	provider.account = ""
	if _, err = resolver.Resolve(context.Background(), request); err == nil {
		t.Fatal("missing verified account must not become tenant account")
	}
}
