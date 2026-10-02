package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	dexec "github.com/viant/datly/exec"
	"github.com/viant/scy/kms"
	"github.com/viant/scy/kms/blowfish"
)

// encToken is the JSON shape stored encrypted in the enc_token column. All
// fields beyond the original four are optional so legacy payloads decode
// unchanged; new writers include them for delegated (per-provider) tokens.
type encToken struct {
	AccessToken  string   `json:"access_token,omitempty"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	IDToken      string   `json:"id_token,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	Issuer       string   `json:"issuer,omitempty"`
	Resource     string   `json:"resource,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	TokenType    string   `json:"token_type,omitempty"`
	Subject      string   `json:"subject,omitempty"`
	ProviderRef  string   `json:"provider_ref,omitempty"`
	ClientRef    string   `json:"client_ref,omitempty"`
	// IDTokenExpiresAt records the verified ID-token exp; ExpiresAt remains the
	// access-token expiry for compatibility.
	IDTokenExpiresAt string `json:"id_token_expires_at,omitempty"`
	// IssuedAt records when the selected token set was obtained, enabling the
	// original-lifetime clamp of the refresh policy.
	IssuedAt string `json:"issued_at,omitempty"`
}

// TokenStoreDAO is a Datly-backed TokenStore with Blowfish encryption.
type TokenStoreDAO struct {
	invoker dexec.ComponentInvoker
	// salt encrypts workspace-provider rows (legacy: the OAuth client
	// configURL). delegatedSalt, when set, encrypts delegated (mcp:v1) rows;
	// it falls back to salt so existing single-key deployments are unchanged.
	salt          string
	delegatedSalt string
	// previousSalts are explicit, allowlisted workspace-token salts accepted
	// only for backward-compatible reads. Delegated rows never use them.
	previousSalts []string
}

// TokenStoreOption configures a TokenStoreDAO.
type TokenStoreOption func(*TokenStoreDAO)

// WithDelegatedSalt sets the encryption salt used for delegated (mcp:v1)
// token rows. Workspace rows keep using the base salt.
func WithDelegatedSalt(salt string) TokenStoreOption {
	return func(s *TokenStoreDAO) { s.delegatedSalt = strings.TrimSpace(salt) }
}

// WithPreviousSalts adds retired workspace-token salts that may be used for
// backward-compatible reads. New writes always use the active canonical salt.
// Delegated provider rows deliberately ignore this list.
func WithPreviousSalts(salts ...string) TokenStoreOption {
	return func(s *TokenStoreDAO) {
		for _, salt := range salts {
			s.addPreviousSalt(salt)
		}
	}
}

// NewTokenStoreDAO creates a Datly-backed token store.
func NewTokenStoreDAO(invoker dexec.ComponentInvoker, salt string, opts ...TokenStoreOption) *TokenStoreDAO {
	rawSalt := strings.TrimSpace(salt)
	store := &TokenStoreDAO{invoker: invoker, salt: canonicalTokenStoreSalt(rawSalt)}
	// Existing rows may have been written before local SCY resource paths were
	// canonicalized. Keep the exact configured text as an allowlisted legacy
	// candidate, but only when it differs from the canonical active salt.
	if rawSalt != "" && rawSalt != store.salt {
		store.addPreviousSalt(rawSalt)
	}
	for _, opt := range opts {
		if opt != nil {
			opt(store)
		}
	}
	return store
}

func (s *TokenStoreDAO) addPreviousSalt(salt string) {
	if s == nil {
		return
	}
	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || candidate == s.salt {
			return
		}
		for _, existing := range s.previousSalts {
			if existing == candidate {
				return
			}
		}
		s.previousSalts = append(s.previousSalts, candidate)
	}
	add(salt)
	add(canonicalTokenStoreSalt(salt))
}

// canonicalTokenStoreSalt mirrors SCY's local secret lookup: a relative file
// resource is resolved beneath $HOME/.secret. Non-file URLs and the key suffix
// (for example |blowfish://default) are preserved verbatim.
func canonicalTokenStoreSalt(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	resource, suffix := raw, ""
	if idx := strings.Index(raw, "|"); idx >= 0 {
		resource, suffix = raw[:idx], raw[idx:]
	} else {
		// NewTokenStoreDAO also accepts opaque application-provided salts and
		// plain local config paths. Only SCY resource|key locators have the
		// well-defined $HOME/.secret relative-path semantics normalized here.
		return raw
	}
	resource = strings.TrimSpace(resource)
	if resource == "" || strings.Contains(resource, "://") {
		return raw
	}
	if filepath.IsAbs(resource) {
		return filepath.Clean(resource) + suffix
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return raw
	}
	return filepath.Join(home, ".secret", resource) + suffix
}

// saltFor selects the encryption salt for a provider row: delegated rows use
// the delegated salt when configured, everything else the base salt.
func (s *TokenStoreDAO) saltFor(provider string) string {
	if s.delegatedSalt != "" && IsDelegatedProviderKey(provider) {
		return s.delegatedSalt
	}
	return s.salt
}

var tokCipher = blowfish.Cipher{}

func (s *TokenStoreDAO) encrypt(ctx context.Context, t *OAuthToken) (string, error) {
	et := encToken{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		IDToken:      t.IDToken,
		Issuer:       t.Issuer,
		Resource:     t.Resource,
		Scopes:       t.Scopes,
		TokenType:    t.TokenType,
		Subject:      t.Subject,
		ProviderRef:  t.ProviderRef,
		ClientRef:    t.ClientRef,
	}
	if !t.ExpiresAt.IsZero() {
		et.ExpiresAt = t.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if !t.IDTokenExpiresAt.IsZero() {
		et.IDTokenExpiresAt = t.IDTokenExpiresAt.Format(time.RFC3339)
	}
	if !t.IssuedAt.IsZero() {
		et.IssuedAt = t.IssuedAt.Format(time.RFC3339)
	}
	b, err := json.Marshal(et)
	if err != nil {
		return "", err
	}
	key := &kms.Key{Kind: "raw", Raw: string(blowfish.EnsureKey([]byte(s.saltFor(t.Provider))))}
	enc, err := tokCipher.Encrypt(ctx, key, b)
	if err != nil {
		return "", err
	}
	return base64RawURL(enc), nil
}

func (s *TokenStoreDAO) decrypt(ctx context.Context, enc, provider string) (*OAuthToken, error) {
	tok, _, err := s.decryptWithPrevious(ctx, enc, provider)
	return tok, err
}

func (s *TokenStoreDAO) decryptWithPrevious(ctx context.Context, enc, provider string) (*OAuthToken, bool, error) {
	raw, err := base64RawURLDecode(enc)
	if err != nil {
		return nil, false, err
	}
	activeSalt := s.saltFor(provider)
	candidates := []string{activeSalt}
	if !IsDelegatedProviderKey(provider) {
		candidates = append(candidates, s.previousSalts...)
	}
	var lastErr error
	for idx, candidate := range candidates {
		key := &kms.Key{Kind: "raw", Raw: string(blowfish.EnsureKey([]byte(candidate)))}
		// The Blowfish implementation decrypts in place. Every candidate must
		// receive an untouched ciphertext copy or a failed active-key attempt
		// would corrupt the bytes used by the allowlisted legacy candidates.
		candidateRaw := append([]byte(nil), raw...)
		dec, decErr := tokCipher.Decrypt(ctx, key, candidateRaw)
		if decErr != nil {
			lastErr = decErr
			continue
		}
		tok, decErr := decodeEncryptedToken(dec)
		if decErr != nil {
			lastErr = decErr
			continue
		}
		return tok, idx > 0, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("tokenstore: no configured decryption salt")
	}
	return nil, false, lastErr
}

func decodeEncryptedToken(dec []byte) (*OAuthToken, error) {
	var et encToken
	if err := json.Unmarshal(dec, &et); err != nil {
		return nil, err
	}
	t := &OAuthToken{
		AccessToken:  et.AccessToken,
		RefreshToken: et.RefreshToken,
		IDToken:      et.IDToken,
		Issuer:       et.Issuer,
		Resource:     et.Resource,
		Scopes:       et.Scopes,
		TokenType:    et.TokenType,
		Subject:      et.Subject,
		ProviderRef:  et.ProviderRef,
		ClientRef:    et.ClientRef,
	}
	if et.ExpiresAt != "" {
		if parsed, pErr := time.Parse(time.RFC3339, et.ExpiresAt); pErr == nil {
			t.ExpiresAt = parsed
		}
	}
	if et.IDTokenExpiresAt != "" {
		if parsed, pErr := time.Parse(time.RFC3339, et.IDTokenExpiresAt); pErr == nil {
			t.IDTokenExpiresAt = parsed
		}
	}
	if et.IssuedAt != "" {
		if parsed, pErr := time.Parse(time.RFC3339, et.IssuedAt); pErr == nil {
			t.IssuedAt = parsed
		}
	}
	return t, nil
}

// tokenRow is one non-empty user_oauth_token row considered by Get.
type tokenRow struct {
	userID   string
	provider string
	enc      string
}

// chooseTokenRow selects the row Get serves for requestedProv: an exact
// provider match always wins (including exact delegated storage keys); when
// none matches, the first non-delegated row is served through the
// instrumented legacy fallback. Delegated (mcp:v1) rows are never fallback
// candidates — a missing workspace-provider row must not be answered with a
// delegated MCP credential.
func chooseTokenRow(rows []tokenRow, requestedProv string) (selected *tokenRow, viaFallback bool) {
	var fallback *tokenRow
	for i := range rows {
		row := &rows[i]
		if requestedProv != "" && row.provider == requestedProv {
			return row, false
		}
		if fallback == nil && !IsDelegatedProviderKey(row.provider) {
			fallback = row
		}
	}
	return fallback, fallback != nil
}

// decryptRow reads using the active salt plus explicitly allowed legacy salts.
// A legacy hit is migrated in place with a ciphertext compare-and-swap, so a
// concurrent refresh is never overwritten. Token material is never logged.
func (s *TokenStoreDAO) decryptRow(ctx context.Context, username, provider, enc string) (*OAuthToken, error) {
	tok, usedPrevious, err := s.decryptWithPrevious(ctx, enc, provider)
	if err != nil || tok == nil {
		return tok, err
	}
	username = strings.TrimSpace(username)
	provider = strings.TrimSpace(provider)
	tok.Username = username
	tok.Provider = provider
	if usedPrevious && s.invoker != nil {
		started := time.Now()
		if migrateErr := s.migrateCiphertext(ctx, username, provider, enc, tok); migrateErr != nil {
			logDatlyStoreOp(ctx, "token", "migrate_encryption", username+"|"+provider, started, migrateErr)
		}
	}
	return tok, nil
}

// helpers

func base64RawURL(b []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "=")
}

func base64RawURLDecode(s string) ([]byte, error) {
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}
