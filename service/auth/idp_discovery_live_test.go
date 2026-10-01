package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/viant/scy/auth/jwt/verifier"
)

// This checks the production discovery path and verifier against public IdP
// metadata. It does not obtain a token or authenticate a user.
func TestOAuthVerifierConfig_LiveDiscoveryAndJWKS(t *testing.T) {
	discoveryURL := strings.TrimSpace(os.Getenv("AGENTLY_TEST_IDP_DISCOVERY_URL"))
	if discoveryURL == "" {
		t.Skip("set AGENTLY_TEST_IDP_DISCOVERY_URL for public live discovery")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := oauthVerifierConfig(ctx, &Config{OAuth: &OAuth{Client: &OAuthClient{DiscoveryURL: discoveryURL}}})
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NotEmpty(t, cfg.CertURL)
	actual := verifier.New(cfg)
	require.NoError(t, actual.Init(ctx))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.CertURL, nil)
	require.NoError(t, err)
	response, err := oauthMetadataHTTPClient(ctx).Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var publicKeys struct {
		Keys []struct {
			Kid       string `json:"kid"`
			Algorithm string `json:"alg"`
		} `json:"keys"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&publicKeys))
	require.NotEmpty(t, publicKeys.Keys)
	require.Equal(t, "RS256", publicKeys.Keys[0].Algorithm)
	require.NotEmpty(t, publicKeys.Keys[0].Kid)
	// A local unrelated key cannot authenticate as this issuer. Validating this
	// dummy token forces the production verifier's lazy JWKS fetch and RSA
	// parsing; require the signature error rather than a network/key lookup error.
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	dummy := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"sub": "local-discovery-fixture", "exp": time.Now().Add(time.Minute).Unix()})
	dummy.Header["kid"] = publicKeys.Keys[0].Kid
	signed, err := dummy.SignedString(wrongKey)
	require.NoError(t, err)
	_, err = actual.Validate(ctx, signed)
	require.ErrorIs(t, err, jwt.ErrTokenSignatureInvalid)
	require.ErrorContains(t, err, "crypto/rsa: verification error")
}
