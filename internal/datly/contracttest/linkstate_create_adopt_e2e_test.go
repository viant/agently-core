package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	xhandler "github.com/viant/xdatly/handler"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	current "github.com/viant/agently-core/internal/datly/oauth/linkstate/read"
	replacement "github.com/viant/agently-core/internal/datly/oauth/linkstate/write"
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

// Creation, pending adoption, competing writes and retries are checked against
// fixed expected winner identities through the generated runtime.
func TestLinkStateCreateAdoptSequentialEvidence(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc               string
		input              bool
		expect             bool
		pending, absent    bool
		race               bool
		concurrent, vanish bool
		exhausted, failed  bool
	}
	for _, tc := range []useCase{{desc: "expired row is replaced", expect: true}, {desc: "consumed row is replaced", input: true, expect: true}, {desc: "pending row is adopted without mutation", pending: true}, {desc: "missing flow is created", absent: true, expect: true}, {desc: "losing insert must adopt stored winner", absent: true, race: true, expect: false}, {desc: "concurrent creator is adopted after fresh Current", absent: true, concurrent: true}, {desc: "concurrent replacement is adopted after fresh Current", concurrent: true}, {desc: "concurrent deletion retries as creation", concurrent: true, vanish: true, expect: true}, {desc: "exhausted replacement adopts last expired winner", absent: true, race: true, exhausted: true}, {desc: "zero insert without winner remains error", absent: true, failed: true}} {
		t.Run(tc.desc, func(t *testing.T) {
			fixture := func() (*sql.DB, string) {
				db, path := goalFixture(t, project)
				if tc.exhausted {
					_, err := db.Exec("PRAGMA journal_mode=WAL")
					must(t, err)
				}
				if tc.concurrent {
					_, err := db.Exec("PRAGMA journal_mode=WAL")
					must(t, err)

				}
				consumed := "NULL"
				expiry := "2026-01-01 00:00:00"
				if tc.pending {
					expiry = "2027-01-01 00:00:00"
				}
				if tc.input {
					consumed = "'2026-01-01 00:00:00'"
					expiry = "2027-01-01 00:00:00"
				}
				if tc.absent {
					if tc.failed {
						_, err := db.Exec("CREATE TRIGGER ignored_flow BEFORE INSERT ON oauth_link_state BEGIN SELECT RAISE(IGNORE); END")
						must(t, err)
					}
					if tc.race {
						raceSQL := `CREATE TRIGGER competing_flow BEFORE INSERT ON oauth_link_state WHEN NEW.state_hash='new' BEGIN
 INSERT INTO oauth_link_state(state_hash,flow_hash,user_id,session_hash,provider,expires_at,consumed_at,created_at) VALUES('winner',NEW.flow_hash,'u9','session9','winner-provider','2027-01-01 00:00:00',NULL,'2026-01-02 00:00:00');
 SELECT RAISE(IGNORE); END;`
						if tc.exhausted {
							raceSQL = strings.ReplaceAll(raceSQL, "2027-01-01 00:00:00", "2026-01-01 00:00:00")
						}
						_, err := db.Exec(raceSQL)
						must(t, err)
					}
					return db, path
				}
				_, err := db.Exec("INSERT INTO oauth_link_state(state_hash,flow_hash,user_id,session_hash,provider,expires_at,consumed_at,created_at) VALUES('old','flow','u1','session1','ap','" + expiry + "'," + consumed + ",'2026-01-01 00:00:00')")
				must(t, err)
				return db, path
			}
			db, newPath := fixture()
			body := `{"data":{"stateHash":"new","flowHash":"flow","userId":"u2","sessionHash":"session2","provider":"bp","expiresAt":"2027-01-01 00:00:00","now":"2026-01-02 00:00:00"}}`
			resources := resource.New()
			must(t, resources.Register(current.ReaderDatlyResourceNamespace, current.ReaderDatlyResources))
			ca := payloadArtifact(t, resources, reflect.TypeFor[current.ReaderComponent](), reflect.TypeFor[current.LinkStateInput](), reflect.TypeFor[current.LinkStateOutput]())
			currentReader, err := ca.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
			must(t, err)
			must(t, resources.Register(replacement.WriteDatlyResourceNamespace, replacement.WriteDatlyResources))
			wa := payloadArtifact(t, resources, reflect.TypeFor[replacement.WriteComponent](), reflect.TypeFor[replacement.Input](), reflect.TypeFor[replacement.Output]())
			views, err := viewprovider.New(viewprovider.Config{Dependencies: wa.ViewDependencies, Input: wa.Input, SQL: &dsql.SQLComponent{DB: db}})
			must(t, err)
			handler, err := writer.New(wa.Component, reflect.TypeFor[replacement.Input](), reflect.TypeFor[replacement.Output](), "patch")
			must(t, err)
			var source dexec.DataSource = dml.Source{DB: db}
			if tc.exhausted {
				attempts := 0
				source = ownershipRaceSource{base: dml.Source{DB: db}, onAttempt: func() error {
					attempts++
					if attempts != 2 {
						return nil
					}
					competitor, err := sql.Open("sqlite3", newPath+"?_foreign_keys=on")
					if err != nil {
						return err
					}
					defer competitor.Close()
					_, err = competitor.Exec("UPDATE oauth_link_state SET state_hash='last-winner',expires_at='2026-01-01 00:00:00' WHERE flow_hash='flow'")
					return err
				}}
			}
			if tc.concurrent {
				source = ownershipRaceSource{base: dml.Source{DB: db}, once: new(sync.Once), before: func() error {
					competitor, err := sql.Open("sqlite3", newPath+"?_foreign_keys=on")
					if err != nil {
						return err
					}
					defer competitor.Close()
					if tc.vanish {
						_, err = competitor.Exec("DELETE FROM oauth_link_state WHERE flow_hash='flow'")
						return err
					}
					_, err = competitor.Exec("INSERT INTO oauth_link_state(state_hash,flow_hash,user_id,session_hash,provider,expires_at,consumed_at,created_at) VALUES('winner','flow','u9','session9','winner-provider','2027-01-01 00:00:00',NULL,'2026-01-02 00:00:00') ON CONFLICT(flow_hash) DO UPDATE SET state_hash=excluded.state_hash,user_id=excluded.user_id,session_hash=excluded.session_hash,provider=excluded.provider,expires_at=excluded.expires_at,consumed_at=excluded.consumed_at,created_at=excluded.created_at")
					return err
				}}
			}
			rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: ca.Component, Input: ca.Input, Output: ca.Output, OutputType: reflect.TypeFor[current.LinkStateOutput](), Reader: currentReader, Providers: []locator.Provider{ordinaryAccess("linkstateaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return true, true, nil })}}, {Component: wa.Component, Input: wa.Input, Output: wa.Output, OutputType: reflect.TypeFor[replacement.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: source}}, druntime.WithResources(resources))
			must(t, err)
			nativeBody := strings.Replace(body, `{"data":{`, `{"data":[{`, 1)
			nativeBody = strings.TrimSuffix(nativeBody, "}") + "]}"
			request := httptest.NewRequest("PATCH", "/v1/internal/agently/user/oauth/linkstate/write", strings.NewReader(nativeBody))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			writeResult, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/internal/agently/user/oauth/linkstate/write", scope)
			if tc.failed {
				if err == nil {
					t.Fatal("zero-row insert without winner succeeded")
				}
				return
			}
			must(t, err)
			if writeResult.(*replacement.Output).Created != tc.expect {
				t.Errorf("native created=%v expected=%v", writeResult.(*replacement.Output).Created, tc.expect)
			}
			input := &current.LinkStateInput{}
			input.SetFlowHash("flow")
			input.SetPending(true)
			result, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: ca.Component.Key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/user/oauth/linkstate/current"}}, Input: input})
			must(t, err)
			data, err := json.Marshal(result.(*current.LinkStateOutput).Data)
			must(t, err)
			var current []json.RawMessage
			must(t, json.Unmarshal(data, &current))
			newRows := normalizeRowsInOrder(t, current)
			expectedHash := "new"
			if tc.pending {
				expectedHash = "old"
			}
			if tc.race || (tc.concurrent && !tc.vanish) {
				expectedHash = "winner"
			}
			if tc.exhausted {
				expectedHash = "last-winner"
			}
			createdRows := writeResult.(*replacement.Output).Data
			if len(createdRows) != 1 || createdRows[0].StateHash != expectedHash {
				t.Errorf("native returned rows=%s; stored winner expected=%q", pretty(createdRows), expectedHash)
			}
			if len(newRows) != 1 || newRows[0]["statehash"] != expectedHash || newRows[0]["consumedat"] != nil {
				t.Fatalf("replacement=%s", pretty(newRows))
			}
			expectedUser, expectedSession, expectedProvider := "u2", "session2", "bp"
			if tc.pending {
				expectedUser, expectedSession, expectedProvider = "u1", "session1", "ap"
			} else if tc.race || (tc.concurrent && !tc.vanish) {
				expectedUser, expectedSession, expectedProvider = "u9", "session9", "winner-provider"
			}
			if newRows[0]["userid"] != expectedUser || newRows[0]["sessionhash"] != expectedSession || newRows[0]["provider"] != expectedProvider {
				t.Fatalf("adoption did not preserve the stored winner identity: %s", pretty(newRows))
			}
		})
	}
}

// Fixture-only barrier: the real generated writer has already bound Current
// in its owned transaction when another connection commits competing state.
type ownershipRaceSource struct {
	base      dml.Source
	before    func() error
	once      *sync.Once
	onAttempt func() error
}

func (s ownershipRaceSource) InvocationKey() any            { return s.base.InvocationKey() }
func (s ownershipRaceSource) InvocationTransactionKey() any { return s.base.InvocationTransactionKey() }
func (s ownershipRaceSource) ResolveSequenceStrategy(ctx context.Context) (dexec.DataSource, string, error) {
	prepared, policy, err := s.base.ResolveSequenceStrategy(ctx)
	if err != nil {
		return nil, "", err
	}
	s.base = prepared.(dml.Source)
	return s, policy, nil
}
func (s ownershipRaceSource) Open(ctx context.Context) (xhandler.Data, error) {
	data, err := s.base.Open(ctx)
	if err != nil {
		return nil, err
	}
	return &ownershipRaceData{Data: data.(*dml.Data), source: s}, nil
}

type ownershipRaceData struct {
	*dml.Data
	source ownershipRaceSource
}

func (d *ownershipRaceData) PrepareCompletion(ctx context.Context) error {
	var err error
	if d.source.onAttempt != nil {
		err = d.source.onAttempt()
	} else {
		d.source.once.Do(func() { err = d.source.before() })
	}
	if err != nil {
		return err
	}
	return d.Data.PrepareCompletion(ctx)
}
