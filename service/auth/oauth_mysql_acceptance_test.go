package auth

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/datly/bootstrap/connector"
	"github.com/viant/datly/standalone"
)

func oauthMySQLRuntimes(t *testing.T) ([]*standalone.Server, *sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	prefix := fmt.Sprintf("authmysql-%d-", time.Now().UnixNano())
	t.Cleanup(func() {
		for _, item := range []struct{ table, key string }{{"user_oauth_token", "user_id"}, {"oauth_link_state", "state_hash"}, {"users", "id"}} {
			_, err := db.Exec("DELETE FROM "+item.table+" WHERE "+item.key+" LIKE ?", prefix+"%")
			require.NoError(t, err)
		}
	})
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "../..")
	servers := make([]*standalone.Server, 2)
	for i := range servers {
		servers[i], err = native.New(context.Background(), native.Options{SourceRoot: root, Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn, MaxOpenConns: 4}}})
		require.NoError(t, err)
		server := servers[i]
		t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	}
	return servers, db, prefix
}
func oauthMySQLRace(action func(int) error) []error {
	start := make(chan struct{})
	errors := make([]error, 2)
	var group sync.WaitGroup
	for i := range errors {
		group.Add(1)
		go func(index int) { defer group.Done(); <-start; errors[index] = action(index) }(i)
	}
	close(start)
	group.Wait()
	return errors
}

func TestTokenStoreMySQLProviderMetadataLeaseAndCASIndependentPools(t *testing.T) {
	servers, db, prefix := oauthMySQLRuntimes(t)
	ctx := context.Background()
	user := prefix + "user"
	_, err := db.Exec("INSERT INTO users(id,username,provider) VALUES(?,?,'local')", user, user)
	require.NoError(t, err)
	var width int
	require.NoError(t, db.QueryRow("SELECT CHARACTER_MAXIMUM_LENGTH FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='user_oauth_token' AND column_name='provider'").Scan(&width))
	require.Equal(t, 128, width)
	stores := []*TokenStoreDAO{NewTokenStoreDAO(servers[0], "disposable-oauth-test-salt"), NewTokenStoreDAO(servers[1], "disposable-oauth-test-salt")}
	providerRef := strings.Repeat("provider-ref-", 128)
	clientRef := strings.Repeat("client-ref-", 64)
	provider := DelegatedProviderStorageKey(prefix, providerRef)
	require.LessOrEqual(t, len(provider), width)
	at := time.Now().UTC().Truncate(time.Second)
	token := &OAuthToken{Username: user, Provider: provider, AccessToken: "fixture-access", RefreshToken: "fixture-refresh", IDToken: "fixture-id", ExpiresAt: at.Add(time.Hour), IDTokenExpiresAt: at.Add(2 * time.Hour), IssuedAt: at, Issuer: "https://fixture.invalid/issuer", Resource: "https://fixture.invalid/resource", Scopes: []string{"read", "write"}, TokenType: "idToken", Subject: "fixture-subject", ProviderRef: providerRef, ClientRef: clientRef}
	require.NoError(t, stores[0].Put(ctx, token))
	loaded, err := stores[1].GetExact(ctx, user, provider)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, providerRef, loaded.ProviderRef)
	require.Equal(t, clientRef, loaded.ClientRef)
	require.Equal(t, token.Scopes, loaded.Scopes)
	require.True(t, loaded.IDTokenExpiresAt.Equal(token.IDTokenExpiresAt))
	// The physical composite key accepts its full width; encrypted metadata is not truncated to that width.
	boundary := *token
	boundary.Provider = strings.Repeat("p", width)
	require.NoError(t, stores[0].Put(ctx, &boundary))
	exact, err := stores[1].GetExact(ctx, user, boundary.Provider)
	require.NoError(t, err)
	require.Equal(t, boundary.Provider, exact.Provider)
	acquired := make([]bool, 2)
	versions := make([]int64, 2)
	owners := []string{prefix + "worker-a", prefix + "worker-b"}
	errs := oauthMySQLRace(func(i int) error {
		var err error
		versions[i], acquired[i], err = stores[i].TryAcquireRefreshLease(ctx, user, provider, owners[i], time.Minute)
		return err
	})
	for _, err := range errs {
		require.NoError(t, err)
	}
	winner := -1
	for i, ok := range acquired {
		if ok {
			require.Equal(t, -1, winner)
			winner = i
		}
	}
	require.NotEqual(t, -1, winner)
	loser := 1 - winner
	require.NoError(t, stores[loser].ReleaseRefreshLease(ctx, user, provider, owners[loser]))
	_, ok, err := stores[loser].TryAcquireRefreshLease(ctx, user, provider, owners[loser], time.Minute)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = stores[loser].CASPut(ctx, token, versions[winner], owners[loser])
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = stores[winner].CASPut(ctx, token, versions[winner]-1, owners[winner])
	require.NoError(t, err)
	require.False(t, ok)
	replaced := make([]bool, 2)
	errs = oauthMySQLRace(func(i int) error {
		next := *token
		next.AccessToken = fmt.Sprintf("fixture-winner-%d", i)
		var err error
		replaced[i], err = stores[i].CASPut(ctx, &next, versions[winner], owners[winner])
		return err
	})
	successes := 0
	for i, err := range errs {
		require.NoError(t, err)
		if replaced[i] {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	var version int64
	require.NoError(t, db.QueryRow("SELECT version FROM user_oauth_token WHERE user_id=? AND provider=?", user, provider).Scan(&version))
	require.Equal(t, versions[winner]+1, version)
	final, err := stores[0].GetExact(ctx, user, provider)
	require.NoError(t, err)
	require.Equal(t, providerRef, final.ProviderRef)
	require.Equal(t, clientRef, final.ClientRef)
	require.Equal(t, token.Subject, final.Subject)
	require.Equal(t, token.Resource, final.Resource)
}

func TestOAuthStateMySQLCompetingCreateConsumeReplayIndependentPools(t *testing.T) {
	servers, _, prefix := oauthMySQLRuntimes(t)
	ctx := context.Background()
	stores := []*OAuthStateStoreNative{NewOAuthStateStoreNative(servers[0]), NewOAuthStateStoreNative(servers[1])}
	records := []*OAuthStateRecord{stateRecord(prefix+"state-a", prefix+"flow", prefix+"owner", prefix+"session", time.Now().UTC().Add(time.Hour)), stateRecord(prefix+"state-b", prefix+"flow", prefix+"owner", prefix+"session", time.Now().UTC().Add(time.Hour))}
	results := make([]*OAuthStateRecord, 2)
	created := make([]bool, 2)
	errs := oauthMySQLRace(func(i int) error {
		var err error
		results[i], created[i], err = stores[i].CreateOrGetPending(ctx, records[i])
		return err
	})
	count := 0
	for i, err := range errs {
		require.NoError(t, err)
		require.NotNil(t, results[i])
		if created[i] {
			count++
		}
	}
	require.Equal(t, 1, count)
	require.Equal(t, results[0].StateHash, results[1].StateHash)
	state := results[0]
	require.ErrorIs(t, stores[0].Consume(ctx, state.StateHash, prefix+"other-owner", state.SessionHash), ErrOAuthStateInvalid)
	require.ErrorIs(t, stores[0].Consume(ctx, state.StateHash, state.CanonicalUserID, prefix+"other-session"), ErrOAuthStateInvalid)
	errs = oauthMySQLRace(func(i int) error {
		return stores[i].Consume(ctx, state.StateHash, state.CanonicalUserID, state.SessionHash)
	})
	winners := 0
	for _, err := range errs {
		if err == nil {
			winners++
		} else {
			require.ErrorIs(t, err, ErrOAuthStateInvalid)
		}
	}
	require.Equal(t, 1, winners)
	require.ErrorIs(t, stores[1].Consume(ctx, state.StateHash, state.CanonicalUserID, state.SessionHash), ErrOAuthStateInvalid)
	require.ErrorIs(t, stores[1].Consume(ctx, prefix+"missing", state.CanonicalUserID, state.SessionHash), ErrOAuthStateInvalid)
	pending, err := stores[0].GetPending(ctx, state.FlowHash)
	require.NoError(t, err)
	require.Nil(t, pending)
	replacement := stateRecord(prefix+"state-next", state.FlowHash, state.CanonicalUserID, state.SessionHash, time.Now().UTC().Add(time.Hour))
	next, inserted, err := stores[1].CreateOrGetPending(ctx, replacement)
	require.NoError(t, err)
	require.True(t, inserted)
	require.Equal(t, replacement.StateHash, next.StateHash)
	require.NoError(t, stores[0].Consume(ctx, next.StateHash, next.CanonicalUserID, next.SessionHash))
	expired := stateRecord(prefix+"expired", prefix+"expired-flow", prefix+"owner", prefix+"session", time.Now().UTC().Add(-time.Minute))
	_, _, err = stores[0].CreateOrGetPending(ctx, expired)
	require.NoError(t, err)
	require.ErrorIs(t, stores[1].Consume(ctx, expired.StateHash, expired.CanonicalUserID, expired.SessionHash), ErrOAuthStateInvalid)
}
