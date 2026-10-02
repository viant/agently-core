package auth

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	read "github.com/viant/agently-core/internal/datly/oauth/token/read"
	write "github.com/viant/agently-core/internal/datly/oauth/token/write"

	"github.com/viant/agently-core/internal/authlog"
	"github.com/viant/agently-core/internal/datly/dbtime"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"github.com/viant/datly/standalone"
	xhandler "github.com/viant/xdatly/handler"
)

var oauthTokenReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/user/oauth"},
}

var oauthTokenWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/user/oauth"},
}

func (s *TokenStoreDAO) readRows(ctx context.Context, input *read.TokenInput) ([]*read.TokenView, error) {
	if s == nil || s.invoker == nil {
		return nil, fmt.Errorf("token store is not configured")
	}
	value, err := s.invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: oauthTokenReaderTarget, Input: input})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.TokenOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("token reader returned %T", value)
	}
	return out.Data, nil
}

func (s *TokenStoreDAO) writeRow(ctx context.Context, input *write.Input) (*write.Output, error) {
	if s == nil || s.invoker == nil {
		return nil, fmt.Errorf("token store is not configured")
	}
	value, err := s.invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: oauthTokenWriterTarget, Input: input})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*write.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("token writer returned %T", value)
	}
	return out, nil
}

func (s *TokenStoreDAO) Get(ctx context.Context, username, provider string) (*OAuthToken, error) {
	if s == nil || s.invoker == nil {
		return nil, nil
	}
	username, provider = strings.TrimSpace(username), strings.TrimSpace(provider)
	started := time.Now()
	var opErr error
	defer func() { logDatlyStoreOp(ctx, "token", "get", username+"|"+provider, started, opErr) }()
	input := &read.TokenInput{}
	input.SetId(username)
	rows, err := s.readRows(ctx, input)
	if err != nil {
		opErr = err
		return nil, err
	}
	candidates := make([]tokenRow, 0, len(rows))
	for _, row := range rows {
		if row == nil || strings.TrimSpace(row.EncToken) == "" {
			continue
		}
		candidates = append(candidates, tokenRow{userID: strings.TrimSpace(row.UserId), provider: strings.TrimSpace(row.Provider), enc: row.EncToken})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].provider < candidates[j].provider })
	selected, viaFallback := chooseTokenRow(candidates, provider)
	if selected == nil {
		return nil, nil
	}
	if viaFallback && provider != "" && provider != selected.provider {
		authlog.Log(ctx, authlog.Event{Op: "token_provider_fallback", UserID: username, Provider: provider, Classification: "provider_fallback", Action: "served_" + selected.provider})
	}
	tok, err := s.decryptRow(ctx, selected.userID, selected.provider, selected.enc)
	if err != nil {
		opErr = err
		return nil, err
	}
	tok.Username = strings.TrimSpace(firstNonEmpty(selected.userID, username))
	tok.Provider = strings.TrimSpace(firstNonEmpty(selected.provider, provider))
	return tok, nil
}

func (s *TokenStoreDAO) GetExact(ctx context.Context, username, provider string) (*OAuthToken, error) {
	if s == nil || s.invoker == nil {
		return nil, nil
	}
	username, provider = strings.TrimSpace(username), strings.TrimSpace(provider)
	if username == "" || provider == "" {
		return nil, nil
	}
	started := time.Now()
	var opErr error
	defer func() { logDatlyStoreOp(ctx, "token", "get_exact", username+"|"+provider, started, opErr) }()
	row, err := s.readExact(ctx, username, provider)
	if err != nil {
		opErr = err
		return nil, err
	}
	if row == nil || strings.TrimSpace(row.EncToken) == "" {
		return nil, nil
	}
	tok, err := s.decryptRow(ctx, username, provider, row.EncToken)
	if err != nil {
		opErr = err
		return nil, err
	}
	tok.Username, tok.Provider = username, provider
	return tok, nil
}

func (s *TokenStoreDAO) readExact(ctx context.Context, username, provider string) (*read.TokenView, error) {
	input := &read.TokenInput{}
	input.SetId(strings.TrimSpace(username))
	input.SetProvider(strings.TrimSpace(provider))
	rows, err := s.readRows(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(rows) > 1 {
		return nil, fmt.Errorf("token reader returned multiple rows for composite identity")
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func (s *TokenStoreDAO) ListDelegated(ctx context.Context, userID string) ([]*OAuthToken, error) {
	if s == nil || s.invoker == nil || strings.TrimSpace(userID) == "" {
		return nil, nil
	}
	userID = strings.TrimSpace(userID)
	started := time.Now()
	var opErr error
	defer func() { logDatlyStoreOp(ctx, "token", "list_delegated", userID+"|", started, opErr) }()
	input := &read.TokenInput{}
	input.SetId(userID)
	rows, err := s.readRows(ctx, input)
	if err != nil {
		opErr = err
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Provider < rows[j].Provider })
	var result []*OAuthToken
	for _, row := range rows {
		if row == nil || strings.TrimSpace(row.EncToken) == "" || !IsDelegatedProviderKey(strings.TrimSpace(row.Provider)) {
			continue
		}
		tok, decErr := s.decryptRow(ctx, userID, row.Provider, row.EncToken)
		if decErr != nil {
			logDatlyStoreOp(ctx, "token", "decrypt", userID+"|"+row.Provider, time.Now(), decErr)
			continue
		}
		tok.Username, tok.Provider = userID, row.Provider
		result = append(result, tok)
	}
	return result, nil
}

// ValidateProviderColumnWidth uses live SQLX metadata through the linked host.
// Unknown metadata fails closed; SQLite's unbounded TEXT needs no width check.
func (s *TokenStoreDAO) ValidateProviderColumnWidth(ctx context.Context, width int) error {
	if s == nil || s.invoker == nil {
		return fmt.Errorf("tokenstore: native runtime is not configured")
	}
	inspector, ok := s.invoker.(interface {
		InspectColumn(context.Context, string, string, string) (*standalone.ColumnInfo, error)
	})
	if !ok {
		return fmt.Errorf("tokenstore: native column metadata is unavailable")
	}
	column, err := inspector.InspectColumn(ctx, "agently", "user_oauth_token", "provider")
	if err != nil {
		return fmt.Errorf("tokenstore: provider column metadata: %w", err)
	}
	switch column.Dialect {
	case "sqlite", "sqlite3":
		return nil
	case "mysql":
		if column.Length != nil && *column.Length < int64(width) {
			return fmt.Errorf("tokenstore: user_oauth_token.provider width %d is below the required %d characters for delegated storage keys", *column.Length, width)
		}
		return nil
	default:
		return fmt.Errorf("tokenstore: unsupported database dialect %q", column.Dialect)
	}
}

func (s *TokenStoreDAO) Put(ctx context.Context, token *OAuthToken) error {
	if s == nil || s.invoker == nil || token == nil {
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
	row := &write.Token{}
	row.SetUserId(strings.TrimSpace(token.Username))
	row.SetProvider(strings.TrimSpace(token.Provider))
	row.SetEncToken(enc)
	input := &write.Input{}
	input.SetToken(row)
	_, opErr = s.writeRow(ctx, input)
	return opErr
}

func (s *TokenStoreDAO) Delete(ctx context.Context, username, provider string) error {
	if s == nil || s.invoker == nil {
		return nil
	}
	username, provider = strings.TrimSpace(username), strings.TrimSpace(provider)
	started := time.Now()
	var opErr error
	defer func() { logDatlyStoreOp(ctx, "token", "delete", username+"|"+provider, started, opErr) }()
	row := &write.Token{}
	row.SetUserId(username)
	row.SetProvider(provider)
	input := &write.Input{}
	input.SetToken(row)
	input.SetMode("clear")
	_, opErr = s.writeRow(ctx, input)
	return opErr
}

func (s *TokenStoreDAO) ScanExpiring(ctx context.Context, horizon time.Time) ([]*OAuthToken, error) {
	if s == nil || s.invoker == nil {
		return nil, nil
	}
	started := time.Now()
	var opErr error
	defer func() { logDatlyStoreOp(ctx, "token", "scan", "|", started, opErr) }()
	rows, err := s.readRows(ctx, &read.TokenInput{})
	if err != nil {
		opErr = err
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UserId == rows[j].UserId {
			return rows[i].Provider < rows[j].Provider
		}
		return rows[i].UserId < rows[j].UserId
	})
	var result []*OAuthToken
	for _, row := range rows {
		if row == nil || strings.TrimSpace(row.EncToken) == "" {
			continue
		}
		tok, decErr := s.decryptRow(ctx, strings.TrimSpace(row.UserId), strings.TrimSpace(row.Provider), row.EncToken)
		if decErr != nil {
			logDatlyStoreOp(ctx, "token", "decrypt", row.UserId+"|"+row.Provider, time.Now(), decErr)
			continue
		}
		if tok == nil || strings.TrimSpace(tok.RefreshToken) == "" || (!tok.ExpiresAt.IsZero() && tok.ExpiresAt.After(horizon)) {
			continue
		}
		tok.Username, tok.Provider = row.UserId, row.Provider
		result = append(result, tok)
	}
	return result, nil
}

func (s *TokenStoreDAO) migrateCiphertext(ctx context.Context, username, provider, oldEnc string, tok *OAuthToken) error {
	if s == nil || s.invoker == nil || tok == nil || strings.TrimSpace(oldEnc) == "" {
		return nil
	}
	newEnc, err := s.encrypt(ctx, tok)
	if err != nil {
		return err
	}
	if newEnc == oldEnc {
		return nil
	}
	row := &write.Token{}
	row.SetUserId(strings.TrimSpace(username))
	row.SetProvider(strings.TrimSpace(provider))
	row.SetEncToken(newEnc)
	input := &write.Input{}
	input.SetToken(row)
	input.SetMode("migrate")
	input.SetExpectedEncToken(oldEnc)
	_, err = s.writeRow(ctx, input)
	var conflict *xhandler.Conflict
	if errors.As(err, &conflict) {
		return nil
	}
	return err
}

func (s *TokenStoreDAO) TryAcquireRefreshLease(ctx context.Context, username, provider, owner string, ttl time.Duration) (int64, bool, error) {
	if s == nil || s.invoker == nil {
		return 0, false, nil
	}
	username, provider, owner = strings.TrimSpace(username), strings.TrimSpace(provider), strings.TrimSpace(owner)
	if username == "" || provider == "" || owner == "" {
		return 0, false, nil
	}
	started := time.Now()
	var opErr error
	defer func() { logDatlyStoreOp(ctx, "token", "lease", username+"|"+provider, started, opErr) }()
	current, err := s.readExact(ctx, username, provider)
	if err != nil {
		opErr = err
		return 0, false, err
	}
	if current == nil {
		return 0, false, nil
	}
	now, err := dbtime.ParseDatabaseUTC(current.DbNow)
	if err != nil {
		opErr = err
		return 0, false, err
	}
	seconds := int64(ttl.Seconds())
	if seconds < 1 {
		seconds = 30
	}
	until := now.Add(time.Duration(seconds) * time.Second)
	row := &write.Token{}
	row.SetUserId(username)
	row.SetProvider(provider)
	row.SetLeaseOwner(&owner)
	row.SetLeaseUntil(&until)
	input := &write.Input{}
	input.SetToken(row)
	input.SetMode("claim")
	input.SetLeaseMode("claim")
	out, err := s.writeRow(ctx, input)
	var conflict *xhandler.Conflict
	if errors.As(err, &conflict) {
		return 0, false, nil
	}
	if err != nil {
		opErr = err
		return 0, false, err
	}
	if out.Data == nil {
		return 0, false, nil
	}
	claimed, err := s.readExact(ctx, username, provider)
	if err != nil {
		opErr = err
		return 0, false, err
	}
	if claimed == nil {
		opErr = fmt.Errorf("tokenstore: claimed row is unavailable")
		return 0, false, opErr
	}
	return claimed.Version, true, nil
}

func (s *TokenStoreDAO) ReleaseRefreshLease(ctx context.Context, username, provider, owner string) error {
	if s == nil || s.invoker == nil {
		return nil
	}
	username, provider, owner = strings.TrimSpace(username), strings.TrimSpace(provider), strings.TrimSpace(owner)
	if username == "" || provider == "" || owner == "" {
		return nil
	}
	started := time.Now()
	var opErr error
	defer func() { logDatlyStoreOp(ctx, "token", "release", username+"|"+provider, started, opErr) }()
	row := &write.Token{}
	row.SetUserId(username)
	row.SetProvider(provider)
	input := &write.Input{}
	input.SetToken(row)
	input.SetMode("release")
	input.SetExpectedLeaseOwner(owner)
	_, err := s.writeRow(ctx, input)
	var conflict *xhandler.Conflict
	if errors.As(err, &conflict) {
		return nil
	}
	opErr = err
	return err
}

func (s *TokenStoreDAO) CASPut(ctx context.Context, token *OAuthToken, expectedVersion int64, owner string) (bool, error) {
	if s == nil || s.invoker == nil || token == nil {
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
	row := &write.Token{}
	row.SetUserId(strings.TrimSpace(token.Username))
	row.SetProvider(strings.TrimSpace(token.Provider))
	row.SetEncToken(enc)
	input := &write.Input{}
	input.SetToken(row)
	input.SetMode("cas_put")
	input.SetExpectedVersion(expectedVersion)
	input.SetExpectedLeaseOwner(strings.TrimSpace(owner))
	out, err := s.writeRow(ctx, input)
	var conflict *xhandler.Conflict
	if errors.As(err, &conflict) {
		return false, nil
	}
	if err != nil {
		opErr = err
		return false, err
	}
	return out.Data != nil, nil
}
