package tests

import (
	"context"
	"database/sql"
	requestprovider "github.com/viant/bindly/provider/request"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestSessionUnifiedMutationAtomicity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct{ reject, caller, commit bool }
	type expect struct {
		failed bool
		ids    []string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"owned mixed update/delete/insert commits", input{}, expect{ids: []string{"s1", "s-new"}}},
		{"late insertion rejection rolls back prior update and deletion", input{reject: true}, expect{failed: true, ids: []string{"s1", "s2"}}},
		{"caller rollback owns mixed mutation", input{caller: true}, expect{ids: []string{"s1", "s2"}}},
		{"caller commit owns mixed mutation", input{caller: true, commit: true}, expect{ids: []string{"s1", "s-new"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := sessionParityFixture(t, project)
			var tx *sql.Tx
			if tc.input.caller {
				db.SetMaxOpenConns(1)
				var err error
				tx, err = db.BeginTx(context.Background(), nil)
				must(t, err)
				defer tx.Rollback()
			}
			rt, _ := sessionParityRuntime(t, db, tx)
			newID := "s-new"
			if tc.input.reject {
				newID = "s-reject"
			}
			body := `{"data":[{"id":"s1","userId":"changed","provider":"local","expiresAt":"2028-01-01T00:00:00Z"},{"id":"s2","shouldDelete":true},{"id":"` + newID + `","userId":"u3","provider":"local","expiresAt":"2028-01-01T00:00:00Z"}]}`
			req := httptest.NewRequest("PATCH", "/v1/api/agently/user/session", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(req)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/user/session", scope)
			if (err != nil) != tc.expect.failed {
				t.Fatalf("mutation error=%v expected failure=%v", err, tc.expect.failed)
			}
			if tx != nil {
				var owner string
				must(t, tx.QueryRow("SELECT user_id FROM session WHERE id='s1'").Scan(&owner))
				if owner != "changed" {
					t.Fatalf("pending owner=%q", owner)
				}
				if tc.input.commit {
					must(t, tx.Commit())
				} else {
					must(t, tx.Rollback())
				}
			}
			rows, err := db.Query("SELECT id FROM session ORDER BY CASE WHEN id='s1' THEN 0 ELSE 1 END,id")
			must(t, err)
			defer rows.Close()
			ids := []string{}
			for rows.Next() {
				var id string
				must(t, rows.Scan(&id))
				ids = append(ids, id)
			}
			must(t, rows.Err())
			if !reflect.DeepEqual(ids, tc.expect.ids) {
				t.Fatalf("stored identities=%v expected=%v", ids, tc.expect.ids)
			}
			var owner string
			must(t, db.QueryRow("SELECT user_id FROM session WHERE id='s1'").Scan(&owner))
			expectedOwner := "changed"
			if tc.expect.failed || (tc.input.caller && !tc.input.commit) {
				expectedOwner = "u1"
			}
			if owner != expectedOwner {
				t.Fatalf("owner=%q expected=%q", owner, expectedOwner)
			}
		})
	}
}
