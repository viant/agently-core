package token

import (
	"context"
	"testing"

	iauth "github.com/viant/agently-core/internal/auth"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

func TestMarshalRestoreSecurityContext_PreservesProvider(t *testing.T) {
	tok := &scyauth.Token{
		Token: oauth2.Token{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
		},
		IDToken: "id-token",
	}
	ctx := iauth.WithProvider(context.Background(), "oauth")
	ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "awitas_viant_devtest"})
	ctx = iauth.WithTokens(ctx, tok)

	data, err := MarshalSecurityContext(ctx)
	if err != nil {
		t.Fatalf("MarshalSecurityContext() error: %v", err)
	}
	restored, sd, err := RestoreSecurityContext(context.Background(), data)
	if err != nil {
		t.Fatalf("RestoreSecurityContext() error: %v", err)
	}
	if sd == nil || sd.Provider != "oauth" {
		t.Fatalf("restored provider = %#v, want oauth", sd)
	}
	if got := iauth.Provider(restored); got != "oauth" {
		t.Fatalf("context provider = %q, want oauth", got)
	}
}
