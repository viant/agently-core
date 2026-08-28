package auth

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/viant/scy/kms"
	"github.com/viant/scy/kms/blowfish"
)

func TestEncTokenMetadataRoundTrip(t *testing.T) {
	dao := NewTokenStoreDAO(nil, "unit-test-salt")
	expires := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	idExpires := time.Date(2026, 8, 28, 14, 30, 0, 0, time.UTC)
	issued := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)
	source := &OAuthToken{
		AccessToken:      "access",
		RefreshToken:     "refresh",
		IDToken:          "id",
		ExpiresAt:        expires,
		Issuer:           "https://idp-dev6.adelphic-dev.com",
		Resource:         "https://mcp6.dev.viant.ai/mcp",
		Scopes:           []string{"plan:create", "plan:edit", "plan:read"},
		TokenType:        "accessToken",
		Subject:          "dev6-subject",
		ProviderRef:      "adelphic-dev6",
		ClientRef:        "steward-web",
		IDTokenExpiresAt: idExpires,
		IssuedAt:         issued,
	}
	enc, err := dao.encrypt(context.Background(), source)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	decoded, err := dao.decrypt(context.Background(), enc, "")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decoded.AccessToken != source.AccessToken || decoded.RefreshToken != source.RefreshToken || decoded.IDToken != source.IDToken {
		t.Fatalf("token values did not survive round trip: %+v", decoded)
	}
	if !decoded.ExpiresAt.Equal(expires) {
		t.Fatalf("expiry did not survive round trip: %v", decoded.ExpiresAt)
	}
	if decoded.Issuer != source.Issuer || decoded.Resource != source.Resource ||
		decoded.TokenType != source.TokenType || decoded.Subject != source.Subject ||
		decoded.ProviderRef != source.ProviderRef || decoded.ClientRef != source.ClientRef {
		t.Fatalf("metadata did not survive round trip: %+v", decoded)
	}
	if len(decoded.Scopes) != 3 || decoded.Scopes[0] != "plan:create" {
		t.Fatalf("scopes did not survive round trip: %v", decoded.Scopes)
	}
	if !decoded.IDTokenExpiresAt.Equal(idExpires) || !decoded.IssuedAt.Equal(issued) {
		t.Fatalf("id-token expiry / issued-at did not survive round trip: %+v", decoded)
	}
}

// TestEncTokenLegacyPayloadCompatibility proves payloads written before the
// metadata extension still decode, and new payloads without metadata keep the
// original JSON field set.
func TestEncTokenLegacyPayloadCompatibility(t *testing.T) {
	dao := NewTokenStoreDAO(nil, "unit-test-salt")
	// Simulate a legacy writer: encrypt the original four-field JSON directly.
	legacy := map[string]string{
		"access_token":  "legacy-access",
		"refresh_token": "legacy-refresh",
		"id_token":      "legacy-id",
		"expires_at":    "2026-08-28T15:00:00Z",
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	key := &kms.Key{Kind: "raw", Raw: string(blowfish.EnsureKey([]byte("unit-test-salt")))}
	enc, err := tokCipher.Encrypt(context.Background(), key, raw)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	decoded, err := dao.decrypt(context.Background(), base64RawURL(enc), "")
	if err != nil {
		t.Fatalf("decrypt legacy payload: %v", err)
	}
	if decoded.AccessToken != "legacy-access" || decoded.RefreshToken != "legacy-refresh" {
		t.Fatalf("legacy payload did not decode: %+v", decoded)
	}
	if decoded.Issuer != "" || decoded.Resource != "" || len(decoded.Scopes) != 0 {
		t.Fatalf("legacy payload must decode with empty optional metadata: %+v", decoded)
	}

	// New writer without metadata emits exactly the legacy JSON field names.
	minimal := &OAuthToken{AccessToken: "a", RefreshToken: "r"}
	encMinimal, err := dao.encrypt(context.Background(), minimal)
	if err != nil {
		t.Fatalf("encrypt minimal: %v", err)
	}
	rawMinimal, err := base64RawURLDecode(encMinimal)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	plain, err := tokCipher.Decrypt(context.Background(), key, rawMinimal)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal(plain, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for field := range fields {
		switch field {
		case "access_token", "refresh_token", "id_token", "expires_at":
		default:
			t.Fatalf("metadata-free payload must stay JSON-compatible with legacy readers; unexpected field %q", field)
		}
	}
}
