package core

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	iauth "github.com/viant/agently-core/internal/auth"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	resource "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/authz/oauth"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestContinuationAcceptsRenewedHTTPPrincipalAndRejectsChangedAuthority(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	var mu sync.Mutex
	roles := []string{"reader"}
	source := "source-1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/jwks" {
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "key", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"sub": "reader", "account": "21", "roles": roles, "source": source})
	}))
	defer server.Close()
	providers, err := oauth.NewHTTPProviders(context.Background(), oauth.HTTPIdentityConfig{Kind: "oidc-userinfo", Issuer: server.URL, Audience: "owned", Algorithms: []string{"RS256"}, JWKSURL: server.URL + "/jwks", JWKSRefreshInterval: "1m", UserInfoURL: server.URL + "/userinfo", FactLease: "30s", TenantForAllAccounts: "workspace", Mapping: oauth.HTTPIdentityMapping{Subject: "sub", AccountID: "account", JWTAccountID: "account", Roles: "roles", AuthorityRevision: "source"}}, nil)
	require.NoError(t, err)
	bearer := func(expiry time.Time) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": server.URL, "aud": "owned", "sub": "reader", "account": "21", "exp": expiry.Unix()})
		token.Header["kid"] = "key"
		value, e := token.SignedString(key)
		require.NoError(t, e)
		return value
	}
	resolver := func(ctx context.Context) (resource.VerifiedActor, error) {
		p, e := providers.Identity.ResolvePrincipal(ctx)
		return resource.VerifiedActor{Issuer: p.Facts.Issuer, Subject: p.Facts.Subject, TenantID: p.Facts.Tenant, AccountID: p.AccountID, IdentityRevision: p.IdentityRevision, ValidUntil: p.Facts.ValidUntil}, e
	}
	store := &contractAnchorClient{conversation: &apiconv.Conversation{CreatedByUserId: ptrContract("reader")}}
	svc := &Service{convClient: store}
	svc.SetContinuationAuthority(resolver, func(ctx context.Context, expected resource.VerifiedActor) error {
		current, e := resolver(ctx)
		if e != nil {
			return e
		}
		if current.Issuer != expected.Issuer || current.Subject != expected.Subject || current.TenantID != expected.TenantID || current.AccountID != expected.AccountID || current.IdentityRevision != expected.IdentityRevision {
			return resource.ErrResourceDenied
		}
		return nil
	})
	base := requestctx.WithConversationID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "reader"}), "contract-conversation")
	first := oauth.WithBearer(base, bearer(time.Now().Add(time.Hour)))
	request := &llm.GenerateRequest{Options: &llm.Options{Metadata: map[string]any{}}}
	input := &GenerateInput{AgentID: "agent", ModelSelection: llm.ModelSelection{Model: "model"}}
	_, err = svc.prepareContinuationContract(first, request, input, contractModel{})
	require.NoError(t, err)
	store.request, err = json.Marshal(request)
	require.NoError(t, err)
	store.conversation.Transcript = []*conversationmodel.TranscriptView{{Message: []*conversationmodel.MessageView{{ModelCall: &conversationmodel.ModelCallView{RequestPayloadId: ptrContract("payload")}}}}}
	renewed := oauth.WithBearer(base, bearer(time.Now().Add(2*time.Hour)))
	_, err = svc.prepareContinuationContract(renewed, &llm.GenerateRequest{Options: &llm.Options{}}, input, contractModel{})
	require.NoError(t, err, "same-facts credential renewal must preserve full-history authority")
	mu.Lock()
	roles = []string{"writer"}
	mu.Unlock()
	_, err = svc.prepareContinuationContract(renewed, &llm.GenerateRequest{Options: &llm.Options{}}, input, contractModel{})
	require.ErrorContains(t, err, "history authority changed")
	mu.Lock()
	roles = []string{"reader"}
	source = "source-2"
	mu.Unlock()
	_, err = svc.prepareContinuationContract(renewed, &llm.GenerateRequest{Options: &llm.Options{}}, input, contractModel{})
	require.ErrorContains(t, err, "history authority changed")
	expired := oauth.WithBearer(base, bearer(time.Now().Add(-time.Minute)))
	_, err = svc.prepareContinuationContract(expired, &llm.GenerateRequest{Options: &llm.Options{}}, input, contractModel{})
	require.Error(t, err)
}
