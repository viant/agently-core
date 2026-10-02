package tests

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	read "github.com/viant/agently-core/internal/datly/oauth/token/read"
	write "github.com/viant/agently-core/internal/datly/oauth/token/write"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

func TestOAuthTokenNativeMutationModes(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	ctx := context.Background()
	for _, tc := range []struct {
		name            string
		seed            string
		mode            string
		provider        string
		enc             string
		expectedEnc     string
		expectedOwner   string
		expectedVersion *int64
		wantError       bool
		wantEnc         string
		wantVersion     int64
		wantLease       string
		wantStatus      string
		claim           bool
	}{
		{name: "claim idle row", mode: "claim", provider: "ap", claim: true, wantEnc: "one-a", wantLease: "worker", wantStatus: "refreshing"},
		{name: "claim expired lease", seed: `UPDATE user_oauth_token SET lease_owner='old',lease_until=DATETIME('now','-1 hour'),refresh_status='refreshing' WHERE user_id='u1' AND provider='ap'`, mode: "claim", provider: "ap", claim: true, wantEnc: "one-a", wantLease: "worker", wantStatus: "refreshing"},
		{name: "claim live lease conflicts", seed: `UPDATE user_oauth_token SET lease_owner='old',lease_until=DATETIME('now','+1 hour'),refresh_status='refreshing' WHERE user_id='u1' AND provider='ap'`, mode: "claim", provider: "ap", claim: true, wantError: true, wantEnc: "one-a", wantLease: "old", wantStatus: "refreshing"},
		{name: "clear retains audit row", mode: "clear", provider: "ap", wantEnc: "", wantVersion: 1, wantStatus: "idle"},
		{name: "clear missing row is no-op", mode: "clear", provider: "missing"},
		{name: "migrate ciphertext with old value", mode: "migrate", provider: "ap", enc: "new-ciphertext", expectedEnc: "one-a", wantEnc: "new-ciphertext", wantVersion: 1, wantStatus: "idle"},
		{name: "migrate stale ciphertext conflicts", mode: "migrate", provider: "ap", enc: "new-ciphertext", expectedEnc: "stale", wantError: true, wantEnc: "one-a", wantStatus: "idle"},
		{name: "release owner clears lease", seed: `UPDATE user_oauth_token SET version=3,lease_owner='worker',lease_until='2027-01-01 00:00:00',refresh_status='refreshing' WHERE user_id='u1' AND provider='ap'`, mode: "release", provider: "ap", expectedOwner: "worker", wantEnc: "one-a", wantVersion: 3, wantStatus: "idle"},
		{name: "release wrong owner conflicts", seed: `UPDATE user_oauth_token SET version=3,lease_owner='worker',lease_until='2027-01-01 00:00:00',refresh_status='refreshing' WHERE user_id='u1' AND provider='ap'`, mode: "release", provider: "ap", expectedOwner: "other", wantError: true, wantEnc: "one-a", wantVersion: 3, wantLease: "worker", wantStatus: "refreshing"},
		{name: "CAS put resets lease and increments version", seed: `UPDATE user_oauth_token SET version=3,lease_owner='worker',lease_until='2027-01-01 00:00:00',refresh_status='refreshing' WHERE user_id='u1' AND provider='ap'`, mode: "cas_put", provider: "ap", enc: "refreshed", expectedOwner: "worker", expectedVersion: int64Pointer(3), wantEnc: "refreshed", wantVersion: 4, wantStatus: "idle"},
		{name: "CAS put stale version conflicts", seed: `UPDATE user_oauth_token SET version=3,lease_owner='worker',lease_until='2027-01-01 00:00:00',refresh_status='refreshing' WHERE user_id='u1' AND provider='ap'`, mode: "cas_put", provider: "ap", enc: "refreshed", expectedOwner: "worker", expectedVersion: int64Pointer(2), wantError: true, wantEnc: "one-a", wantVersion: 3, wantLease: "worker", wantStatus: "refreshing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := oauthTokenParityFixture(t, project)
			if tc.seed != "" {
				_, err := db.Exec(tc.seed)
				must(t, err)
			}
			rt, readerKey := oauthTokenParityRuntime(t, db)
			row := &write.Token{}
			row.SetUserId("u1")
			row.SetProvider(tc.provider)
			if tc.enc != "" {
				row.SetEncToken(tc.enc)
			}
			if tc.claim {
				owner := "worker"
				until := time.Now().UTC().Add(90 * time.Second)
				row.SetLeaseOwner(&owner)
				row.SetLeaseUntil(&until)
			}
			input := &write.Input{}
			input.SetToken(row)
			input.SetMode(tc.mode)
			if tc.claim {
				input.SetLeaseMode("claim")
			}
			if tc.expectedEnc != "" {
				input.SetExpectedEncToken(tc.expectedEnc)
			}
			if tc.expectedOwner != "" {
				input.SetExpectedLeaseOwner(tc.expectedOwner)
			}
			if tc.expectedVersion != nil {
				input.SetExpectedVersion(*tc.expectedVersion)
			}
			_, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/user/oauth"}}, Input: input})
			if (err != nil) != tc.wantError {
				t.Fatalf("mutation error=%v want error=%v", err, tc.wantError)
			}
			query := &read.TokenInput{}
			query.SetId("u1")
			query.SetProvider(tc.provider)
			value, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: readerKey, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/user/oauth"}}, Input: query})
			must(t, err)
			rows := value.(*read.TokenOutput).Data
			if tc.provider == "missing" {
				if len(rows) != 0 {
					t.Fatalf("missing token created: %+v", rows)
				}
				return
			}
			if len(rows) != 1 {
				t.Fatalf("token rows=%d", len(rows))
			}
			got := rows[0]
			lease := ""
			if got.LeaseOwner != nil {
				lease = *got.LeaseOwner
			}
			if got.EncToken != tc.wantEnc || got.Version != tc.wantVersion || lease != tc.wantLease || got.RefreshStatus != tc.wantStatus {
				t.Fatalf("stored token=%+v want enc=%q version=%d lease=%q status=%q", got, tc.wantEnc, tc.wantVersion, tc.wantLease, tc.wantStatus)
			}
		})
	}
}

func int64Pointer(value int64) *int64 { return &value }

func TestOAuthTokenClearJoinsCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := oauthTokenParityFixture(t, project)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	must(t, err)
	rt, _ := oauthTokenParityRuntime(t, db, tx)
	row := &write.Token{}
	row.SetUserId("u1")
	row.SetProvider("ap")
	input := &write.Input{}
	input.SetToken(row)
	input.SetMode("clear")
	_, err = rt.InvokeComponent(ctx, dexec.ComponentRequest{
		Target: dexec.ComponentTarget{
			Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
			Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/user/oauth"},
		}, Input: input,
	})
	must(t, err)
	must(t, tx.Rollback())
	query := &read.TokenInput{}
	query.SetId("u1")
	query.SetProvider("ap")
	value, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{
		Target: dexec.ComponentTarget{
			Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
			Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/user/oauth"},
		}, Input: query,
	})
	must(t, err)
	rows := value.(*read.TokenOutput).Data
	if len(rows) != 1 || rows[0].EncToken != "one-a" || rows[0].Version != 0 {
		t.Fatalf("caller rollback did not preserve token: %+v", rows)
	}
}
