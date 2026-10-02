package tests

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/viant/agently-core/internal/store/oauthlinkstate"
)

func TestOAuthStateStoreNativeLifecycle(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	type input struct {
		seedExpiry string
		seedUsed   bool
		wrongUser  bool
	}
	type expect struct {
		created bool
		state   string
		consume bool
		deleted int64
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{desc: "new flow is created and consumed once", expect: expect{created: true, state: "new", consume: true}},
		{desc: "pending flow is adopted", input: input{seedExpiry: "2027-01-01 00:00:00"}, expect: expect{state: "old", consume: true}},
		{desc: "expired flow is replaced", input: input{seedExpiry: "2026-01-01 00:00:00"}, expect: expect{created: true, state: "new", consume: true}},
		{desc: "consumed flow is replaced", input: input{seedExpiry: "2027-01-01 00:00:00", seedUsed: true}, expect: expect{created: true, state: "new", consume: true}},
		{desc: "wrong owner is rejected", input: input{wrongUser: true}, expect: expect{created: true, state: "new"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := goalFixture(t, project)
			if tc.input.seedExpiry != "" {
				var consumed any
				if tc.input.seedUsed {
					consumed = "2026-01-01 00:00:00"
				}
				_, err := db.Exec(`INSERT INTO oauth_link_state(state_hash,flow_hash,user_id,session_hash,provider,expires_at,consumed_at,created_at) VALUES(?,?,?,?,?,?,?,?)`, "old", "flow", "u1", "session1", "idp", tc.input.seedExpiry, consumed, "2026-01-01 00:00:00")
				must(t, err)
			}
			rt, _ := linkStateCleanupRuntime(t, db)
			store := &oauthlinkstate.Store{Invoker: rt, Now: func() time.Time { return now }}
			incoming := &oauthlinkstate.Record{StateHash: "new", FlowHash: "flow", CanonicalUserID: "u1", SessionHash: "session1", Provider: "idp", ExpiresAt: now.Add(24 * time.Hour)}
			stored, created, err := store.CreateOrGetPending(context.Background(), incoming)
			must(t, err)
			if created != tc.expect.created || stored.StateHash != tc.expect.state {
				t.Fatalf("create/adopt = (%+v, %v), expected %+v", stored, created, tc.expect)
			}
			pending, err := store.GetPending(context.Background(), "flow")
			must(t, err)
			if pending == nil || pending.StateHash != tc.expect.state {
				t.Fatalf("pending = %+v, expected %q", pending, tc.expect.state)
			}
			user := "u1"
			if tc.input.wrongUser {
				user = "u2"
			}
			err = store.Consume(context.Background(), tc.expect.state, user, "session1")
			if (err == nil) != tc.expect.consume {
				t.Fatalf("consume = %v, success expected = %v", err, tc.expect.consume)
			}
			if !tc.expect.consume && !errors.Is(err, oauthlinkstate.ErrInvalidState) {
				t.Fatalf("rejection leaked cause: %v", err)
			}
			if tc.expect.consume {
				if err := store.Consume(context.Background(), tc.expect.state, "u1", "session1"); !errors.Is(err, oauthlinkstate.ErrInvalidState) {
					t.Fatalf("replay = %v, want non-enumerable rejection", err)
				}
				pending, err = store.GetPending(context.Background(), "flow")
				must(t, err)
				if pending != nil {
					t.Fatalf("consumed row remained pending: %+v", pending)
				}
			}
		})
	}
	for _, tc := range []useCase{
		{desc: "expired rows removed at boundary", input: input{seedExpiry: "2026-01-02 00:00:00"}, expect: expect{deleted: 1}},
		{desc: "future row retained", input: input{seedExpiry: "2027-01-01 00:00:00"}},
	} {
		t.Run("cleanup "+tc.desc, func(t *testing.T) {
			db, _ := goalFixture(t, project)
			_, err := db.Exec(`INSERT INTO oauth_link_state(state_hash,flow_hash,user_id,session_hash,provider,expires_at,created_at) VALUES('old','flow','u1','session1','idp',?,'2026-01-01 00:00:00')`, tc.input.seedExpiry)
			must(t, err)
			rt, _ := linkStateCleanupRuntime(t, db)
			store := &oauthlinkstate.Store{Invoker: rt, Now: func() time.Time { return now }}
			deleted, oldest, err := store.DeleteExpired(context.Background(), now)
			must(t, err)
			if deleted != tc.expect.deleted {
				t.Fatalf("deleted = %d, want %d", deleted, tc.expect.deleted)
			}
			if (deleted == 0) != oldest.IsZero() {
				t.Fatalf("oldest = %s with %d deletions", oldest, deleted)
			}
			if deleted > 0 && !oldest.Equal(now) {
				t.Fatalf("oldest = %s, want %s", oldest, now)
			}
			rows := []string{}
			for _, flow := range linkStateRemainingFlows(t, db) {
				rows = append(rows, flow)
			}
			want := []string{}
			if deleted == 0 {
				want = []string{"flow"}
			}
			if !reflect.DeepEqual(rows, want) {
				t.Fatalf("remaining = %v, want %v", rows, want)
			}
		})
	}
}
