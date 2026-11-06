package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuntimeDoesNotExposeJWTUtilities(t *testing.T) {
	root := t.TempDir()
	privateKey, publicKey := generateRSAKeyPair(t, root)
	config := fmt.Sprintf("auth:\n  enabled: true\n  jwt:\n    enabled: true\n    rsa: [%q]\n    rsaPrivateKey: %q\n", publicKey, privateKey)
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte(config), 0600))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime, err := NewRuntime(ctx, root, nil)
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/protected", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "test-user", RuntimeUserFromContext(r.Context()).Subject)
		w.WriteHeader(http.StatusNoContent)
	})
	handler := WithAuthExtensions(mux, runtime)
	token := signTestJWT(t, privateKey, map[string]interface{}{"sub": "test-user"}, time.Hour)
	for _, credential := range []string{"", token} {
		for _, path := range []string{"/v1/api/auth/jwt/mint", "/v1/api/auth/jwt/keypair"} {
			t.Run(path+fmt.Sprint(credential != ""), func(t *testing.T) {
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"subject":"arbitrary-user","bits":2048}`))
				r.RemoteAddr = "203.0.113.10:12345"
				if credential != "" {
					r.Header.Set("Authorization", "Bearer "+credential)
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				require.Equal(t, http.StatusNotFound, w.Code)
				require.NotContains(t, w.Body.String(), "token")
				require.NotContains(t, w.Body.String(), "PRIVATE KEY")
			})
		}
	}
	for _, authenticated := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodGet, "/v1/protected", nil)
		if authenticated {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if authenticated {
			require.Equal(t, http.StatusNoContent, w.Code)
		} else {
			require.Equal(t, http.StatusUnauthorized, w.Code)
		}
	}
}
