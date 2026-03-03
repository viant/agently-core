package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/scy/kms"
	"github.com/viant/scy/kms/blowfish"

	oauthread "github.com/viant/agently-core/pkg/agently/user/oauth"
	oauthwrite "github.com/viant/agently-core/pkg/agently/user/oauth/write"
)

// encToken is the minimal JSON shape stored encrypted in the enc_token column.
type encToken struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

// TokenStoreDAO is a Datly-backed TokenStore with Blowfish encryption.
type TokenStoreDAO struct {
	dao  *datly.Service
	salt string
}

// NewTokenStoreDAO creates a Datly-backed token store.
func NewTokenStoreDAO(dao *datly.Service, salt string) *TokenStoreDAO {
	return &TokenStoreDAO{dao: dao, salt: salt}
}

var tokCipher = blowfish.Cipher{}

func (s *TokenStoreDAO) encrypt(ctx context.Context, t *OAuthToken) (string, error) {
	et := encToken{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		IDToken:      t.IDToken,
	}
	if !t.ExpiresAt.IsZero() {
		et.ExpiresAt = t.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
	}
	b, err := json.Marshal(et)
	if err != nil {
		return "", err
	}
	key := &kms.Key{Kind: "raw", Raw: string(blowfish.EnsureKey([]byte(s.salt)))}
	enc, err := tokCipher.Encrypt(ctx, key, b)
	if err != nil {
		return "", err
	}
	return base64RawURL(enc), nil
}

func (s *TokenStoreDAO) decrypt(ctx context.Context, enc string) (*OAuthToken, error) {
	raw, err := base64RawURLDecode(enc)
	if err != nil {
		return nil, err
	}
	key := &kms.Key{Kind: "raw", Raw: string(blowfish.EnsureKey([]byte(s.salt)))}
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
	}
	if et.ExpiresAt != "" {
		if parsed, pErr := time.Parse(time.RFC3339, et.ExpiresAt); pErr == nil {
			t.ExpiresAt = parsed
		}
	}
	return t, nil
}

// Get loads and decrypts a token from DB.
func (s *TokenStoreDAO) Get(ctx context.Context, username, provider string) (*OAuthToken, error) {
	if s == nil || s.dao == nil {
		return nil, nil
	}
	out := &oauthread.TokenOutput{}
	in := oauthread.TokenInput{}
	in.Has = &oauthread.TokenInputHas{Id: true}
	in.Id = username
	if _, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", oauthread.TokenPathURI)), datly.WithInput(&in), datly.WithOutput(out)); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 || out.Data[0] == nil {
		return nil, nil
	}
	var row *oauthread.TokenView
	if strings.TrimSpace(provider) != "" {
		for _, r := range out.Data {
			if r != nil && strings.TrimSpace(r.Provider) == strings.TrimSpace(provider) {
				row = r
				break
			}
		}
	}
	if row == nil {
		row = out.Data[0]
	}
	if row == nil || strings.TrimSpace(row.EncToken) == "" {
		return nil, nil
	}
	tok, err := s.decrypt(ctx, row.EncToken)
	if err != nil {
		return nil, err
	}
	tok.Username = username
	tok.Provider = provider
	return tok, nil
}

// Put encrypts and saves a token via the Datly write handler.
func (s *TokenStoreDAO) Put(ctx context.Context, token *OAuthToken) error {
	if s == nil || s.dao == nil || token == nil {
		return nil
	}
	enc, err := s.encrypt(ctx, token)
	if err != nil {
		return err
	}
	in := &oauthwrite.Input{Token: &oauthwrite.Token{}}
	in.Token.SetUserID(token.Username)
	in.Token.SetProvider(token.Provider)
	in.Token.SetEncToken(enc)
	out := &oauthwrite.Output{}
	_, err = s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", oauthwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out))
	return err
}

// Delete removes a token by upserting an empty enc_token.
func (s *TokenStoreDAO) Delete(ctx context.Context, username, provider string) error {
	if s == nil || s.dao == nil {
		return nil
	}
	in := &oauthwrite.Input{Token: &oauthwrite.Token{}}
	in.Token.SetUserID(username)
	in.Token.SetProvider(provider)
	in.Token.SetEncToken("")
	out := &oauthwrite.Output{}
	_, err := s.dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", oauthwrite.PathURI)), datly.WithInput(in), datly.WithOutput(out))
	return err
}

// db returns a raw *sql.DB from the datly connector.
func (s *TokenStoreDAO) db() (*sql.DB, error) {
	conn, err := s.dao.Resource().Connector("agently")
	if err != nil {
		return nil, fmt.Errorf("tokenstore: connector lookup: %w", err)
	}
	return conn.DB()
}

// TryAcquireRefreshLease atomically attempts to acquire a distributed lease for
// refreshing the token identified by (username, provider). The lease is granted
// only when the row is idle or has an expired lease. All timestamp comparisons
// use the DB server's CURRENT_TIMESTAMP to avoid clock-skew issues.
func (s *TokenStoreDAO) TryAcquireRefreshLease(ctx context.Context, username, provider, owner string, ttl time.Duration) (int64, bool, error) {
	if s == nil || s.dao == nil {
		return 0, false, nil
	}
	db, err := s.db()
	if err != nil {
		return 0, false, err
	}

	ttlSeconds := int64(ttl.Seconds())
	if ttlSeconds < 1 {
		ttlSeconds = 30
	}

	// Atomically acquire the lease: only succeeds if idle or lease expired.
	res, err := db.ExecContext(ctx,
		`UPDATE user_oauth_token
		 SET lease_owner = ?, lease_until = DATETIME('now', '+' || ? || ' seconds'), refresh_status = 'refreshing'
		 WHERE user_id = ? AND provider = ?
		   AND (refresh_status = 'idle' OR lease_until < DATETIME('now'))`,
		owner, ttlSeconds, username, provider,
	)
	if err != nil {
		return 0, false, fmt.Errorf("tokenstore: acquire lease: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
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
	db, err := s.db()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`UPDATE user_oauth_token
		 SET lease_owner = NULL, lease_until = NULL, refresh_status = 'idle'
		 WHERE user_id = ? AND provider = ? AND lease_owner = ?`,
		username, provider, owner,
	)
	return err
}

// CASPut atomically updates the token only if the current version matches
// expectedVersion and the lease is held by owner. On success, bumps version
// and clears the lease. Returns (true, nil) if the swap succeeded.
func (s *TokenStoreDAO) CASPut(ctx context.Context, token *OAuthToken, expectedVersion int64, owner string) (bool, error) {
	if s == nil || s.dao == nil || token == nil {
		return false, nil
	}
	enc, err := s.encrypt(ctx, token)
	if err != nil {
		return false, err
	}
	db, err := s.db()
	if err != nil {
		return false, err
	}

	res, err := db.ExecContext(ctx,
		`UPDATE user_oauth_token
		 SET enc_token = ?, updated_at = DATETIME('now'), version = version + 1,
		     lease_owner = NULL, lease_until = NULL, refresh_status = 'idle'
		 WHERE user_id = ? AND provider = ? AND version = ? AND lease_owner = ?`,
		enc, token.Username, token.Provider, expectedVersion, owner,
	)
	if err != nil {
		return false, fmt.Errorf("tokenstore: CAS put: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
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
