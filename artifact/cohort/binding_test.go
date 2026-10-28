package cohort

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/viant/agently-core/app/executor"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

type cohortPrincipal struct{ value gating.Principal }

func (p cohortPrincipal) ResolvePrincipal(context.Context) (gating.Principal, error) {
	return p.value, nil
}

func TestCohortFactsUseOnlyVerifiedPrincipalAndAccountScope(t *testing.T) {
	ctx := context.Background()
	facts := authz.Facts{Subject: "alice", Issuer: "verified", Tenant: "tenant", Roles: []string{"operator"}, MembershipGroups: []string{"studio"}, Exposures: []string{"connectors"}, EntityGroups: authz.EntityGroups{"project": {"101"}}, ValidUntil: time.Now().Add(time.Hour)}
	provider := verifiedCohortFacts{source: cohortPrincipal{value: gating.Principal{Facts: facts, AccountID: "account"}}, tenant: "tenant", account: "account"}
	got, err := provider.Resolve(ctx)
	if err != nil || got.Subject != "alice" || len(got.MembershipGroups) != 1 || len(got.Exposures) != 1 || len(got.EntityGroups["project"]) != 1 {
		t.Fatalf("verified facts lost: %+v %v", got, err)
	}
	provider.account = "other"
	if _, err := provider.Resolve(ctx); err == nil {
		t.Fatal("foreign account facts admitted")
	}
	if _, err := verifiedCohortSession(ctx); err == nil {
		t.Fatal("unverified context acquired session binding")
	}
}

func TestCohortRegistrationFailsClosedWithoutHostClientOrVerifiedPrincipal(t *testing.T) {
	if _, err := PrepareBinding(context.Background(), &executor.Runtime{}, Registration{ConnectionName: "studio", ProviderIdentity: "studio", Tenant: "tenant", Account: "account"}); err == nil {
		t.Fatal("missing host client/verified facts accepted")
	}
}

func TestArtifactCohortNamespaceClaimsComeFromConfiguredPrimitiveNamespaces(t *testing.T) {
	claims, err := artifactCohortNamespaceClaims([]string{"ns://catalog", "ns://catalog", "other"})
	if err != nil || len(claims) != 2 || claims[0] != "catalog" || claims[1] != "other" {
		t.Fatalf("unexpected trusted claims: %v %v", claims, err)
	}
	for _, invalid := range []string{"ns://", "ns://catalog/other", "ns://catalog?other", " catalog"} {
		if _, err := artifactCohortNamespaceClaims([]string{invalid}); err == nil {
			t.Fatalf("malformed namespace claim accepted: %q", invalid)
		}
	}
}

func TestCohortHostClientRejectsChangedWorkspaceEndpointBeforeTokenUse(t *testing.T) {
	manager, err := mcpmgr.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	configured := &mcpcfg.MCPClient{PrimitiveProviderIdentity: "catalog-management", ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "http://127.0.0.1:8096/mcp"}}}}
	if err = manager.RegisterLocal(context.Background(), "catalog-management", configured, func(context.Context) (mcpclient.Interface, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	rt := &executor.Runtime{MCPManager: manager}
	called := false
	_, _, err = NewHostClient(context.Background(), rt, "catalog-management", "catalog-management", "http://127.0.0.1:18096/mcp", "", func(context.Context) (string, error) { called = true; return "secret", nil })
	if err == nil || called {
		t.Fatal("host token reached changed endpoint")
	}
}

func TestLocalCohortConfigUsesSecretReferencesAndStrictFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cohort.yaml")
	body := fmt.Sprintf("connectionName: catalog-management\nproviderIdentity: catalog-management\nexpectedEndpoint: http://127.0.0.1:8096/mcp\norigin: http://127.0.0.1:8095\noauthClient: %s\nuserSecret: %s\nkey: blowfish://default\nexpectedSubject: verified-user\ncohort: verified-management\n", filepath.Join(t.TempDir(), "idp.enc"), filepath.Join(t.TempDir(), "user.enc"))
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := loadArtifactCohortLocalConfig(path)
	if err != nil || config.ConnectionName != "catalog-management" || len(config.Scopes) != 1 || config.Scopes[0] != "openid" {
		t.Fatalf("strict config: %+v %v", config, err)
	}
	if err := os.WriteFile(path, []byte(body+"browserRole: admin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadArtifactCohortLocalConfig(path); err == nil {
		t.Fatal("unknown browser-controlled grant field accepted")
	}
	exp := time.Now().Add(time.Hour).Unix()
	claim := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp)))
	got, err := unverifiedTokenExpiry("header." + claim + ".signature")
	if err != nil || got.Unix() != exp {
		t.Fatalf("refresh hint parse: %v %v", got, err)
	}
	if _, err := unverifiedTokenExpiry("not-a-jwt"); err == nil {
		t.Fatal("invalid token expiry accepted")
	}
}
