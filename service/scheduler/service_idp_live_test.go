package scheduler

import (
	"context"
	"os"
	"strings"
	"testing"

	iauth "github.com/viant/agently-core/internal/auth"
	agentsvc "github.com/viant/agently-core/service/agent"
)

// This opt-in check exercises the scheduler's real OOB authorization path.
// It never starts a scheduled job or prints credentials or token values.
func TestService_applyUserCred_LiveIdP(t *testing.T) {
	credentials := strings.TrimSpace(os.Getenv("AGENTLY_SCHEDULER_IDP_CREDENTIALS"))
	client := strings.TrimSpace(os.Getenv("AGENTLY_SCHEDULER_IDP_CLIENT"))
	if credentials == "" || client == "" {
		t.Skip("set AGENTLY_SCHEDULER_IDP_CREDENTIALS and AGENTLY_SCHEDULER_IDP_CLIENT to secret references")
	}
	scopes := strings.Fields(os.Getenv("AGENTLY_SCHEDULER_IDP_SCOPES"))
	if len(scopes) == 0 {
		scopes = []string{"openid"}
	}
	svc := New(nil, &agentsvc.Service{}, WithUserCredAuthConfig(&UserCredAuthConfig{
		Mode: "bff", ClientConfigURL: client, Scopes: scopes,
	}))
	ctx, err := svc.applyUserCred(context.Background(), credentials)
	if err != nil {
		// applyUserCred deliberately returns a sanitized error.
		t.Fatal(err)
	}
	if err := validateSchedulerAuthContext(ctx, scopes, nil); err != nil {
		t.Fatal("authorized scheduler context did not contain usable scoped credentials")
	}
	if iauth.TokensFromContext(ctx) == nil || strings.TrimSpace(iauth.Bearer(ctx)) == "" {
		t.Fatal("authorized scheduler context did not contain an OAuth token and bearer")
	}
}
