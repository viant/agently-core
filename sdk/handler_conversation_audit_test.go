package sdk

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	svcauth "github.com/viant/agently-core/service/auth"
)

func TestConversationAuditUsesServerPrincipal(t *testing.T) {
	for _, signed := range []bool{false, true} {
		var actor string
		mux := http.NewServeMux()
		registerOptionalRoutes(mux, &handlerConfig{authCfg: &svcauth.Config{Enabled: signed}, conversationAuditHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor = authctx.EffectiveUserID(r.Context())
			w.WriteHeader(http.StatusNoContent)
		})})
		r := httptest.NewRequest("GET", "/v1/api/conversation-audit/turns?userId=someone-else", nil)
		r.AddCookie(&http.Cookie{Name: anonymousUserCookieName, Value: "anonymous:audit-example"})
		expected := "anonymous:audit-example"
		if signed {
			expected = "verified-user"
			r = r.WithContext(authctx.WithUserInfo(r.Context(), &authctx.UserInfo{Subject: expected}))
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		require.Equal(t, http.StatusNoContent, w.Code)
		require.Equal(t, expected, actor)
		if signed {
			w = httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("GET", "/v1/api/conversation-audit/capabilities?userId=verified-user", nil))
			require.Equal(t, http.StatusUnauthorized, w.Code)
		}
	}
}
