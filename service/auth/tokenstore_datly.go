package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/scy/kms"
	"github.com/viant/scy/kms/blowfish"

	"github.com/viant/agently-core/internal/authlog"
	oauthwrite "github.com/viant/agently-core/pkg/agently/user/oauth/write"
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
	dao *datly.Service
	// salt encrypts workspace-provider rows (legacy: the OAuth client
	// configURL). delegatedSalt, when set, encrypts delegated (mcp:v1) rows;
	// it falls back to salt so existing single-key deployments are unchanged.
	salt          string
	delegatedSalt string
	mu            sync.RWMutex
	dbCache       *sql.DB
	dialect       string
}

// TokenStoreOption configures a TokenStoreDAO.
type TokenStoreOption func(*TokenStoreDAO)

// WithDelegatedSalt sets the encryption salt used for delegated (mcp:v1)
// token rows. Workspace rows keep using the base salt.
func WithDelegatedSalt(salt string) TokenStoreOption {
	return func(s *TokenStoreDAO) { s.delegatedSalt = strings.TrimSpace(salt) }
}

// NewTokenStoreDAO creates a Datly-backed token store.
func NewTokenStoreDAO(dao *datly.Service, salt string, opts ...TokenStoreOption) *TokenStoreDAO {
	store := &TokenStoreDAO{dao: dao, salt: salt}
	for _, opt := range opts {
		if opt != nil {
			opt(store)
		}
	}
	return store
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
	raw, err := base64RawURLDecode(enc)
	if err != nil {
		return nil, err
	}
	key := &kms.Key{Kind: "raw", Raw: string(blowfish.EnsureKey([]byte(s.saltFor(provider))))}
	dec, err := tokCipher.Decrypt(ctx, key, raw)
	if err != nil {
		return nil, err
	}
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

// Get loads and decrypts a token from DB.
func (s *TokenStoreDAO) Get(ctx context.Context, username, provider string) (*OAuthToken, error) {
	if s == nil || s.dao == nil {
		return nil, nil
	}
	started := time.Now()
	var opErr error
	defer func() {
		logDatlyStoreOp(ctx, "token", "get", strings.TrimSpace(username)+"|"+strings.TrimSpace(provider), started, opErr)
	}()
	db, err := s.db()
	if err != nil {
		opErr = err
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT user_id, provider, enc_token
FROM user_oauth_token
WHERE user_id = ?
ORDER BY provider`,
		strings.TrimSpace(username),
	)
	if err != nil {
		opErr = err
		return nil, err
	}
	defer rows.Close()
	var candidates []tokenRow
	for rows.Next() {
		var userID, rowProvider, rowEnc string
		if err := rows.Scan(&userID, &rowProvider, &rowEnc); err != nil {
			logDatlyStoreOp(ctx, "token", "get_row", strings.TrimSpace(username)+"|"+strings.TrimSpace(provider), time.Now(), err)
			return nil, err
		}
		if strings.TrimSpace(rowEnc) == "" {
			continue
		}
		candidates = append(candidates, tokenRow{userID: strings.TrimSpace(userID), provider: strings.TrimSpace(rowProvider), enc: rowEnc})
	}
	if err := rows.Err(); err != nil {
		opErr = err
		return nil, err
	}
	requestedProv := strings.TrimSpace(provider)
	selected, viaFallback := chooseTokenRow(candidates, requestedProv)
	if selected == nil {
		return nil, nil
	}
	if viaFallback && requestedProv != "" && requestedProv != selected.provider {
		// Instrument every fallback hit so remaining alias dependencies can be
		// inventoried before the generic fallback is removed. Delegated MCP
		// storage keys are never served through this path — delegated callers
		// use GetExact. Never log token material here.
		authlog.Log(ctx, authlog.Event{
			Op:             "token_provider_fallback",
			UserID:         strings.TrimSpace(username),
			Provider:       requestedProv,
			Classification: "provider_fallback",
			Action:         "served_" + selected.provider,
		})
	}
	tok, err := s.decrypt(ctx, selected.enc, selected.provider)
	if err != nil {
		logDatlyStoreOp(ctx, "token", "decrypt", selected.userID+"|"+selected.provider, time.Now(), err)
		return nil, err
	}
	tok.Username = strings.TrimSpace(firstNonEmpty(selected.userID, username))
	tok.Provider = strings.TrimSpace(firstNonEmpty(selected.provider, provider))
	return tok, nil
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

// GetExact loads and decrypts the token stored under exactly (username,
// provider). Unlike Get it never falls back to another provider row: a miss is
// a miss. Delegated MCP credential code must use this method exclusively.
func (s *TokenStoreDAO) GetExact(ctx context.Context, username, provider string) (*OAuthToken, error) {
	if s == nil || s.dao == nil {
		return nil, nil
	}
	username = strings.TrimSpace(username)
	provider = strings.TrimSpace(provider)
	if username == "" || provider == "" {
		return nil, nil
	}
	started := time.Now()
	var opErr error
	defer func() {
		logDatlyStoreOp(ctx, "token", "get_exact", username+"|"+provider, started, opErr)
	}()
	db, err := s.db()
	if err != nil {
		opErr = err
		return nil, err
	}
	var enc string
	row := db.QueryRowContext(ctx,
		`SELECT enc_token FROM user_oauth_token WHERE user_id = ? AND provider = ?`,
		username, provider)
	if err := row.Scan(&enc); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		opErr = err
		return nil, err
	}
	if strings.TrimSpace(enc) == "" {
		return nil, nil
	}
	tok, err := s.decrypt(ctx, enc, provider)
	if err != nil {
		opErr = err
		return nil, err
	}
	tok.Username = username
	tok.Provider = provider
	return tok, nil
}

// ListDelegated returns every delegated (mcp:v1) token row stored for a
// canonical user. It implements DelegatedTokenLister for the user-lifecycle
// cleanup hook; workspace rows are never included.
func (s *TokenStoreDAO) ListDelegated(ctx context.Context, userID string) ([]*OAuthToken, error) {
	if s == nil || s.dao == nil {
		return nil, nil
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, nil
	}
	started := time.Now()
	var opErr error
	defer func() {
		logDatlyStoreOp(ctx, "token", "list_delegated", userID+"|", started, opErr)
	}()
	db, err := s.db()
	if err != nil {
		opErr = err
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT user_id, provider, enc_token FROM user_oauth_token
		 WHERE user_id = ? AND enc_token != '' ORDER BY provider`, userID)
	if err != nil {
		opErr = err
		return nil, err
	}
	defer rows.Close()
	var result []*OAuthToken
	for rows.Next() {
		var rowUser, rowProvider, rowEnc string
		if err := rows.Scan(&rowUser, &rowProvider, &rowEnc); err != nil {
			opErr = err
			return nil, err
		}
		if !IsDelegatedProviderKey(strings.TrimSpace(rowProvider)) {
			continue
		}
		tok, decErr := s.decrypt(ctx, rowEnc, strings.TrimSpace(rowProvider))
		if decErr != nil {
			logDatlyStoreOp(ctx, "token", "decrypt", userID+"|"+strings.TrimSpace(rowProvider), time.Now(), decErr)
			continue
		}
		tok.Username = strings.TrimSpace(rowUser)
		tok.Provider = strings.TrimSpace(rowProvider)
		result = append(result, tok)
	}
	opErr = rows.Err()
	return result, opErr
}

// ValidateProviderColumnWidth verifies the live user_oauth_token.provider
// column can hold at least width characters (the fixed 71-character delegated
// storage key). MySQL silently truncating a delegated key is never accepted;
// dialects without declared column widths (sqlite) pass.
func (s *TokenStoreDAO) ValidateProviderColumnWidth(ctx context.Context, width int) error {
	if s == nil || s.dao == nil {
		return fmt.Errorf("tokenstore: dao is not configured")
	}
	dialect, err := s.dbDialect()
	if err != nil {
		return err
	}
	if dialect != "mysql" {
		return nil
	}
	db, err := s.db()
	if err != nil {
		return err
	}
	var maxLen sql.NullInt64
	row := db.QueryRowContext(ctx,
		`SELECT character_maximum_length FROM information_schema.columns
		 WHERE table_schema = DATABASE() AND table_name = 'user_oauth_token' AND column_name = 'provider'`)
	if err := row.Scan(&maxLen); err != nil {
		return fmt.Errorf("tokenstore: provider column width lookup: %w", err)
	}
	if maxLen.Valid && maxLen.Int64 < int64(width) {
		return fmt.Errorf("tokenstore: user_oauth_token.provider width %d is below the required %d characters for delegated storage keys", maxLen.Int64, width)
	}
	return nil
}

// Put encrypts and saves a token via the Datly write handler.
func (s *TokenStoreDAO) Put(ctx context.Context, token *OAuthToken) error {
	if s == nil || s.dao == nil || token == nil {
		return nil
	}
	started := time.Now()
	var opErr error
	defer func() {
		logDatlyStoreOp(ctx, "token", "put", strings.TrimSpace(token.Username)+"|"+strings.TrimSpace(token.Provider), started, opErr)
	}()
	enc, err := s.encrypt(ctx, token)
	if err != nil {
		opErr = err
		return err
	}
	in := &oauthwrite.Input{Token: &oauthwrite.Token{}}
	in.Token.SetUserID(token.Username)
	in.Token.SetProvider(token.Provider)
	in.Token.SetEncToken(enc)
	out := &oauthwrite.Output{}
	_, err = s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", oauthwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out))
	opErr = err
	return err
}

// Delete atomically clears a token while retaining its audit row.
func (s *TokenStoreDAO) Delete(ctx context.Context, username, provider string) error {
	if s == nil || s.dao == nil {
		return nil
	}
	started := time.Now()
	var opErr error
	defer func() {
		logDatlyStoreOp(ctx, "token", "delete", strings.TrimSpace(username)+"|"+strings.TrimSpace(provider), started, opErr)
	}()
	db, err := s.db()
	if err != nil {
		opErr = err
		return err
	}
	dialect, err := s.dbDialect()
	if err != nil {
		opErr = err
		return err
	}
	query, err := deleteTokenSQL(dialect)
	if err != nil {
		opErr = err
		return err
	}
	_, err = db.ExecContext(ctx, query,
		strings.TrimSpace(username), strings.TrimSpace(provider),
	)
	opErr = err
	return err
}

// ScanExpiring returns all stored tokens expiring before horizon that carry a
// refresh token. Called by the background watcher to refresh tokens for idle
// users who have no active in-memory session.
func (s *TokenStoreDAO) ScanExpiring(ctx context.Context, horizon time.Time) ([]*OAuthToken, error) {
	if s == nil || s.dao == nil {
		return nil, nil
	}
	started := time.Now()
	var opErr error
	defer func() {
		logDatlyStoreOp(ctx, "token", "scan", "|", started, opErr)
	}()
	db, err := s.db()
	if err != nil {
		opErr = err
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT user_id, provider, enc_token FROM user_oauth_token
		 WHERE enc_token != ''
		 ORDER BY user_id`)
	if err != nil {
		opErr = err
		return nil, err
	}
	defer rows.Close()
	var result []*OAuthToken
	for rows.Next() {
		var userID, provider, encTok string
		if err := rows.Scan(&userID, &provider, &encTok); err != nil {
			logDatlyStoreOp(ctx, "token", "scan_row", "|", time.Now(), err)
			continue
		}
		tok, err := s.decrypt(ctx, encTok, provider)
		if err != nil {
			logDatlyStoreOp(ctx, "token", "decrypt", strings.TrimSpace(userID)+"|"+strings.TrimSpace(provider), time.Now(), err)
			continue
		}
		if tok == nil {
			continue
		}
		// Only include tokens that have a refresh token and are near expiry.
		if strings.TrimSpace(tok.RefreshToken) == "" {
			continue
		}
		if !tok.ExpiresAt.IsZero() && tok.ExpiresAt.After(horizon) {
			continue
		}
		tok.Username = userID
		tok.Provider = provider
		result = append(result, tok)
	}
	opErr = rows.Err()
	return result, opErr
}

// db returns a raw *sql.DB from the datly connector.
func (s *TokenStoreDAO) db() (*sql.DB, error) {
	if s == nil || s.dao == nil {
		return nil, nil
	}
	s.mu.RLock()
	if s.dbCache != nil {
		db := s.dbCache
		s.mu.RUnlock()
		return db, nil
	}
	s.mu.RUnlock()
	conn, err := s.dao.Resource().Connector("agently")
	if err != nil {
		return nil, fmt.Errorf("tokenstore: connector lookup: %w", err)
	}
	db, err := conn.DB()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.dbCache == nil {
		s.dbCache = db
	}
	cached := s.dbCache
	s.mu.Unlock()
	return cached, nil
}

func (s *TokenStoreDAO) dbDialect() (string, error) {
	s.mu.RLock()
	if s.dialect != "" {
		dialect := s.dialect
		s.mu.RUnlock()
		return dialect, nil
	}
	s.mu.RUnlock()
	db, err := s.db()
	if err != nil {
		return "", err
	}
	if db == nil {
		return "", fmt.Errorf("tokenstore: nil db")
	}
	dialect, err := detectDBDialect(db.Driver())
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.dialect == "" {
		s.dialect = dialect
	}
	cached := s.dialect
	s.mu.Unlock()
	return cached, nil
}

func detectDBDialect(d interface{}) (string, error) {
	if d == nil {
		return "", fmt.Errorf("tokenstore: nil db driver")
	}
	driverType := fmt.Sprintf("%T", d)
	switch {
	case strings.Contains(driverType, "mysql"):
		return "mysql", nil
	case strings.Contains(driverType, "sqlite"):
		return "sqlite", nil
	default:
		return "", fmt.Errorf("tokenstore: unsupported db driver %s", driverType)
	}
}

func refreshLeaseSQL(dialect string) (string, error) {
	switch dialect {
	case "mysql":
		return `UPDATE user_oauth_token
		 SET lease_owner = ?, lease_until = UTC_TIMESTAMP() + INTERVAL ? SECOND, refresh_status = 'refreshing'
		 WHERE user_id = ? AND provider = ?
		   AND (refresh_status = 'idle' OR lease_until IS NULL OR lease_until < UTC_TIMESTAMP())`, nil
	case "sqlite":
		return `UPDATE user_oauth_token
		 SET lease_owner = ?, lease_until = DATETIME('now', '+' || ? || ' seconds'), refresh_status = 'refreshing'
		 WHERE user_id = ? AND provider = ?
		   AND (refresh_status = 'idle' OR lease_until IS NULL OR lease_until < DATETIME('now'))`, nil
	default:
		return "", fmt.Errorf("tokenstore: unsupported dialect %q", dialect)
	}
}

func casPutSQL(dialect string) (string, error) {
	switch dialect {
	case "mysql":
		return `UPDATE user_oauth_token
		 SET enc_token = ?, updated_at = UTC_TIMESTAMP(), version = version + 1,
		     lease_owner = NULL, lease_until = NULL, refresh_status = 'idle'
		 WHERE user_id = ? AND provider = ? AND version = ? AND lease_owner = ?`, nil
	case "sqlite":
		return `UPDATE user_oauth_token
		 SET enc_token = ?, updated_at = DATETIME('now'), version = version + 1,
		     lease_owner = NULL, lease_until = NULL, refresh_status = 'idle'
		 WHERE user_id = ? AND provider = ? AND version = ? AND lease_owner = ?`, nil
	default:
		return "", fmt.Errorf("tokenstore: unsupported dialect %q", dialect)
	}
}

func deleteTokenSQL(dialect string) (string, error) {
	switch dialect {
	case "mysql":
		return `UPDATE user_oauth_token
		 SET enc_token = '', updated_at = UTC_TIMESTAMP(), version = version + 1,
		     lease_owner = NULL, lease_until = NULL, refresh_status = 'idle'
		 WHERE user_id = ? AND provider = ?`, nil
	case "sqlite":
		return `UPDATE user_oauth_token
		 SET enc_token = '', updated_at = DATETIME('now'), version = version + 1,
		     lease_owner = NULL, lease_until = NULL, refresh_status = 'idle'
		 WHERE user_id = ? AND provider = ?`, nil
	default:
		return "", fmt.Errorf("tokenstore: unsupported dialect %q", dialect)
	}
}

// TryAcquireRefreshLease atomically attempts to acquire a distributed lease for
// refreshing the token identified by (username, provider). The lease is granted
// only when the row is idle or has an expired lease. All timestamp comparisons
// use the DB server's CURRENT_TIMESTAMP to avoid clock-skew issues.
func (s *TokenStoreDAO) TryAcquireRefreshLease(ctx context.Context, username, provider, owner string, ttl time.Duration) (int64, bool, error) {
	if s == nil || s.dao == nil {
		return 0, false, nil
	}
	started := time.Now()
	var opErr error
	defer func() {
		logDatlyStoreOp(ctx, "token", "lease", strings.TrimSpace(username)+"|"+strings.TrimSpace(provider), started, opErr)
	}()
	db, err := s.db()
	if err != nil {
		opErr = err
		return 0, false, err
	}
	dialect, err := s.dbDialect()
	if err != nil {
		opErr = err
		return 0, false, err
	}

	ttlSeconds := int64(ttl.Seconds())
	if ttlSeconds < 1 {
		ttlSeconds = 30
	}

	// Atomically acquire the lease: only succeeds if idle or lease expired.
	query, err := refreshLeaseSQL(dialect)
	if err != nil {
		opErr = err
		return 0, false, err
	}
	res, err := db.ExecContext(ctx, query, owner, ttlSeconds, username, provider)
	if err != nil {
		opErr = err
		return 0, false, fmt.Errorf("tokenstore: acquire lease: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		opErr = err
		return 0, false, err
	}
	if n == 0 {
		return 0, false, nil
	}

	// Read the current version.
	var version int64
	if err := db.QueryRowContext(ctx,
		`SELECT version FROM user_oauth_token WHERE user_id = ? AND provider = ?`,
		username, provider,
	).Scan(&version); err != nil {
		opErr = err
		return 0, false, fmt.Errorf("tokenstore: read version: %w", err)
	}

	return version, true, nil
}

// ReleaseRefreshLease releases a previously acquired lease, resetting the row to idle.
// The owner check ensures we only release our own lease.
func (s *TokenStoreDAO) ReleaseRefreshLease(ctx context.Context, username, provider, owner string) error {
	if s == nil || s.dao == nil {
		return nil
	}
	started := time.Now()
	var opErr error
	defer func() {
		logDatlyStoreOp(ctx, "token", "release", strings.TrimSpace(username)+"|"+strings.TrimSpace(provider), started, opErr)
	}()
	db, err := s.db()
	if err != nil {
		opErr = err
		return err
	}
	_, err = db.ExecContext(ctx,
		`UPDATE user_oauth_token
		 SET lease_owner = NULL, lease_until = NULL, refresh_status = 'idle'
		 WHERE user_id = ? AND provider = ? AND lease_owner = ?`,
		username, provider, owner,
	)
	opErr = err
	return err
}

// CASPut atomically updates the token only if the current version matches
// expectedVersion and the lease is held by owner. On success, bumps version
// and clears the lease. Returns (true, nil) if the swap succeeded.
func (s *TokenStoreDAO) CASPut(ctx context.Context, token *OAuthToken, expectedVersion int64, owner string) (bool, error) {
	if s == nil || s.dao == nil || token == nil {
		return false, nil
	}
	started := time.Now()
	var opErr error
	defer func() {
		logDatlyStoreOp(ctx, "token", "cas_put", strings.TrimSpace(token.Username)+"|"+strings.TrimSpace(token.Provider), started, opErr)
	}()
	enc, err := s.encrypt(ctx, token)
	if err != nil {
		opErr = err
		return false, err
	}
	db, err := s.db()
	if err != nil {
		opErr = err
		return false, err
	}
	dialect, err := s.dbDialect()
	if err != nil {
		opErr = err
		return false, err
	}

	query, err := casPutSQL(dialect)
	if err != nil {
		opErr = err
		return false, err
	}
	res, err := db.ExecContext(ctx, query, enc, token.Username, token.Provider, expectedVersion, owner)
	if err != nil {
		opErr = err
		return false, fmt.Errorf("tokenstore: CAS put: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		opErr = err
		return false, err
	}
	return n == 1, nil
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
