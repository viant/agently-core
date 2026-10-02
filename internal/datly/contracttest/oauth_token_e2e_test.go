package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	oauthread "github.com/viant/agently-core/internal/datly/oauth/token/read"
	oauthwrite "github.com/viant/agently-core/internal/datly/oauth/token/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
)

func TestOAuthTokenLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		body    string
		filters map[string]any
	}
	type expect struct {
		failed bool
		ids    [][2]string
		fields map[[2]string]map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	insertion := `{"userId":"u3","provider":"ap","encToken":"new"}`
	for _, tc := range []useCase{
		{desc: "reader returns full composite identity grid", expect: expect{}},
		{desc: "user predicate retains that user's providers", input: input{filters: map[string]any{"userId": "u1"}}, expect: expect{ids: [][2]string{{"u1", "ap"}, {"u1", "bp"}}}},
		{desc: "unknown user returns no records", input: input{filters: map[string]any{"userId": "absent"}}, expect: expect{ids: [][2]string{}}},
		{desc: "new token receives creation time", input: input{body: `{"data":` + insertion + `}`}, expect: expect{ids: [][2]string{{"u1", "ap"}, {"u1", "bp"}, {"u2", "ap"}, {"u2", "bp"}, {"u3", "ap"}}, fields: map[[2]string]map[string]any{{"u3", "ap"}: {"createdat": "<generated timestamp>", "updatedat": nil, "enctoken": "new"}}}},
		{desc: "supplied creation time is replaced on insert", input: input{body: `{"data":{"userId":"u3","provider":"ap","encToken":"new","createdAt":"2026-01-01T00:00:00Z"}}`}, expect: expect{ids: [][2]string{{"u1", "ap"}, {"u1", "bp"}, {"u2", "ap"}, {"u2", "bp"}, {"u3", "ap"}}, fields: map[[2]string]map[string]any{{"u3", "ap"}: {"createdat": "<generated timestamp>"}}}},
		{desc: "composite update cannot cross user provider pairs", input: input{body: `{"data":{"userId":"u1","provider":"bp","encToken":"changed"}}`}, expect: expect{fields: map[[2]string]map[string]any{{"u1", "bp"}: {"enctoken": "changed", "createdat": "2026-01-01T00:00:00Z", "updatedat": "<generated timestamp>"}, {"u1", "ap"}: {"enctoken": "one-a"}, {"u2", "bp"}: {"enctoken": "two-b"}}}},
		{desc: "omitted token payload retains legacy forced marker", input: input{body: `{"data":{"userId":"u1","provider":"ap"}}`}, expect: expect{fields: map[[2]string]map[string]any{{"u1", "ap"}: {"enctoken": ""}}}},
		{desc: "explicit empty provider is a supplied composite identity", input: input{body: `{"data":{"userId":"u3","provider":"","encToken":"empty-provider"}}`}, expect: expect{ids: [][2]string{{"u1", "ap"}, {"u1", "bp"}, {"u2", "ap"}, {"u2", "bp"}, {"u3", ""}}}},
		{desc: "supplied update time remains authoritative", input: input{body: `{"data":{"userId":"u1","provider":"ap","encToken":"changed","updatedAt":"2026-01-01T00:00:00Z"}}`}, expect: expect{fields: map[[2]string]map[string]any{{"u1", "ap"}: {"updatedat": "2026-01-01T00:00:00Z"}}}},
		{desc: "null update time receives legacy server value", input: input{body: `{"data":{"userId":"u1","provider":"ap","encToken":"changed","updatedAt":null}}`}, expect: expect{fields: map[[2]string]map[string]any{{"u1", "ap"}: {"updatedat": "<generated timestamp>"}}}},
		{desc: "nil body is a no-op", input: input{body: `{"data":null}`}, expect: expect{}},
		{desc: "missing user reference fails", input: input{body: `{"data":{"userId":"absent","provider":"ap","encToken":"new"}}`}, expect: expect{failed: true}},
		{desc: "database update rejection preserves every pair", input: input{body: `{"data":{"userId":"u1","provider":"ap","encToken":"reject"}}`}, expect: expect{failed: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := oauthTokenParityFixture(t, project)
			db, _ := oauthTokenParityFixture(t, project)
			filters := tc.input.filters
			if filters == nil {
				filters = map[string]any{}
			}
			probe := struct {
				Component, DBPath, Body string
				Filters                 map[string]any
			}{"oauthToken", oldPath, tc.input.body, filters}
			payload, err := json.Marshal(probe)
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			var stderr bytes.Buffer
			process.Stderr = &stderr
			raw, err := process.Output()
			if err != nil {
				t.Fatalf("legacy execution: %v\n%s", err, raw)
			}
			var before probeResult
			if err := json.Unmarshal(raw, &before); err != nil {
				t.Fatalf("legacy response: %v\nstdout=%s\nstderr=%s", err, raw, stderr.String())
			}
			rt, key := oauthTokenParityRuntime(t, db)
			var afterOutput json.RawMessage
			var mutationError error
			if tc.input.body != "" {
				req := httptest.NewRequest("PATCH", "/v1/api/agently/user/oauth", strings.NewReader(tc.input.body))
				req.Header.Set("Content-Type", "application/json")
				scope, err := requestprovider.New(req)
				must(t, err)
				defer scope.Close()
				result, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/user/oauth", scope)
				mutationError = err
				if err == nil {
					afterOutput, err = json.Marshal(result.(*oauthwrite.Output).Data)
					must(t, err)
				}
			}
			if before.Failed != tc.expect.failed || (mutationError != nil) != tc.expect.failed {
				t.Fatalf("outcome legacy=%v (%s) v1=%v expected failure=%v", before.Failed, before.Error, mutationError, tc.expect.failed)
			}
			readerInput := &oauthread.TokenInput{Has: &oauthread.TokenInputHas{}}
			if value, present := filters["userId"]; present {
				encoded, err := json.Marshal(value)
				must(t, err)
				must(t, json.Unmarshal(encoded, &readerInput.Id))
				readerInput.Has.Id = true
			}
			output, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/user/oauth"}}, Input: readerInput})
			must(t, err)
			data, err := json.Marshal(output.(*oauthread.TokenOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(data, &rows))
			oldRows, newRows := normalizeOAuthTokenRows(t, before.Rows), normalizeOAuthTokenRows(t, rows)
			// The canonical v1 reader also carries native lease/CAS state for
			// migrated root callers. Compare SDK0's original token columns here.
			for _, row := range newRows {
				delete(row, "version")
				delete(row, "leaseowner")
				delete(row, "leaseuntil")
				delete(row, "refreshstatus")
				delete(row, "dbnow")
			}
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("stored row parity\nlegacy=%s\nv1=%s", pretty(oldRows), pretty(newRows))
			}
			ids := [][2]string{}
			indexed := map[[2]string]map[string]any{}
			for _, row := range newRows {
				key := [2]string{row["userid"].(string), row["provider"].(string)}
				ids = append(ids, key)
				indexed[key] = row
			}
			expectedIDs := tc.expect.ids
			if expectedIDs == nil {
				expectedIDs = [][2]string{{"u1", "ap"}, {"u1", "bp"}, {"u2", "ap"}, {"u2", "bp"}}
			}
			if !reflect.DeepEqual(ids, expectedIDs) {
				t.Fatalf("identities=%v expected=%v", ids, expectedIDs)
			}
			for id, fields := range tc.expect.fields {
				for field, value := range fields {
					if !reflect.DeepEqual(indexed[id][field], value) {
						t.Errorf("%s.%s=%v expected=%v", id, field, indexed[id][field], value)
					}
				}
			}
			if tc.input.body != "" && !tc.expect.failed {
				old := []json.RawMessage{before.Output}
				new := []json.RawMessage{afterOutput}
				legacyResponse := normalizeRowsInOrder(t, old)
				nativeResponse := normalizeRowsInOrder(t, new)
				for _, row := range nativeResponse {
					delete(row, "version")
					delete(row, "leaseowner")
					delete(row, "leaseuntil")
					delete(row, "refreshstatus")
				}
				if !reflect.DeepEqual(legacyResponse, nativeResponse) {
					t.Fatalf("write response parity\nlegacy=%s\nv1=%s", before.Output, afterOutput)
				}
			}
		})
	}
}

func TestOAuthTokenReaderProviderAndLeaseState(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := oauthTokenParityFixture(t, project)
	_, err := db.Exec(`UPDATE user_oauth_token SET version=7,lease_owner='worker',
		lease_until='2026-02-01 00:00:00',refresh_status='refreshing'
		WHERE user_id='u1' AND provider='ap'`)
	must(t, err)
	rt, key := oauthTokenParityRuntime(t, db)
	input := &oauthread.TokenInput{}
	input.SetId("u1")
	input.SetProvider("ap")
	value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{
		Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/user/oauth"}},
		Input:  input,
	})
	must(t, err)
	rows := value.(*oauthread.TokenOutput).Data
	if len(rows) != 1 || rows[0].Version != 7 || rows[0].LeaseOwner == nil || *rows[0].LeaseOwner != "worker" || rows[0].LeaseUntil == nil || rows[0].RefreshStatus != "refreshing" || rows[0].DbNow == "" {
		t.Fatalf("token lease/CAS projection = %+v", rows)
	}
}

func oauthTokenParityFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO users(id,username) VALUES('u1','user1'),('u2','user2'),('u3','user3');
 INSERT INTO user_oauth_token(user_id,provider,enc_token,created_at,updated_at)
 VALUES('u1','ap','one-a','2026-01-01 00:00:00','2026-01-01 00:00:00'),
 ('u1','bp','one-b','2026-01-01 00:00:00',NULL),
 ('u2','ap','two-a','2026-01-01 00:00:00',NULL),
 ('u2','bp','two-b','2026-01-01 00:00:00',NULL);
 CREATE TRIGGER reject_oauth_update BEFORE UPDATE ON user_oauth_token WHEN NEW.enc_token='reject' BEGIN SELECT RAISE(ABORT,'fixture token rejection'); END;`)
	must(t, err)
	return db, path
}

func oauthTokenParityRuntime(t *testing.T, db *sql.DB, supplied ...*sql.Tx) (*druntime.Runtime, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(oauthread.ReaderDatlyResourceNamespace, oauthread.ReaderDatlyResources))
	must(t, resources.Register(oauthwrite.WriterDatlyResourceNamespace, oauthwrite.WriterDatlyResources))
	ra := payloadArtifact(t, resources, reflect.TypeFor[oauthread.ReaderComponent](), reflect.TypeFor[oauthread.TokenInput](), reflect.TypeFor[oauthread.TokenOutput]())
	wa := payloadArtifact(t, resources, reflect.TypeFor[oauthwrite.WriterComponent](), reflect.TypeFor[oauthwrite.Input](), reflect.TypeFor[oauthwrite.Output]())
	reader, err := ra.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: wa.ViewDependencies, Input: wa.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(wa.Component, reflect.TypeFor[oauthwrite.Input](), reflect.TypeFor[oauthwrite.Output](), "patch")
	must(t, err)
	var tx *sql.Tx
	if len(supplied) > 0 {
		tx = supplied[0]
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: ra.Component, Input: ra.Input, Output: ra.Output, OutputType: reflect.TypeFor[oauthread.TokenOutput](), Reader: reader},
		{Component: wa.Component, Input: wa.Input, Output: wa.Output, OutputType: reflect.TypeFor[oauthwrite.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db, Tx: tx}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, ra.Component.Key
}

func normalizeOAuthTokenRows(t *testing.T, rows []json.RawMessage) []map[string]any {
	result := normalizeRowsInOrder(t, rows)
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if left["userid"] == right["userid"] {
			return left["provider"].(string) < right["provider"].(string)
		}
		return left["userid"].(string) < right["userid"].(string)
	})
	return result
}
