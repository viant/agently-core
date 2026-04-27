package auth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func fakeJWTWithExp(t *testing.T, exp time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"exp": exp.Unix()})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"
}

func TestResolveTokenExpiry_ExplicitValueWins(t *testing.T) {
	jwtExp := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	explicit := time.Now().Add(30 * time.Minute).UTC().Truncate(time.Second)
	got := resolveTokenExpiry(explicit.Format(time.RFC3339), fakeJWTWithExp(t, jwtExp), "")
	if !got.Equal(explicit) {
		t.Fatalf("resolveTokenExpiry() = %v, want explicit expiry %v", got, explicit)
	}
}

func TestResolveTokenExpiry_FallsBackToJWTExp(t *testing.T) {
	jwtExp := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	got := resolveTokenExpiry("", fakeJWTWithExp(t, jwtExp), "")
	if !got.Equal(jwtExp) {
		t.Fatalf("resolveTokenExpiry() = %v, want jwt expiry %v", got, jwtExp)
	}
}
