package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/viant/datly/runtime/handler/provider"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	read "github.com/viant/agently-core/internal/datly/reporting/run/read"
	write "github.com/viant/agently-core/internal/datly/reporting/run/write"
	runstore "github.com/viant/agently-core/internal/store/reporting/run"
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

func reportRunFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO report_run(report_run_id,owner_id,materializer,origin,status,started_at,completed_at,revision,ui_run_request_id,report_spec_json,created_at,updated_at) VALUES
 ('running','u1','test','interactive','running','2026-01-01 00:00:00',NULL,1,'req-running',X'7B7D','2026-01-01 00:00:00','2026-01-01 00:00:00'),
 ('manual','u1','test','manual','completed','2026-01-01 00:00:00','2026-01-02 00:00:00',2,'req-manual',X'7B7D','2026-01-01 00:00:00','2026-01-02 00:00:00'),
 ('foreign','u2','test','manual','completed','2026-01-01 00:00:00','2026-01-02 00:00:00',1,'req-foreign',X'7B7D','2026-01-01 00:00:00','2026-01-02 00:00:00')`)
	must(t, err)
	return db, path
}

func reportRunRuntime(t *testing.T, db *sql.DB, subject string, internal bool, supplied *sql.Tx) (*druntime.Runtime, spec.Key, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	must(t, resources.Register(write.WriterDatlyResourceNamespace, write.WriterDatlyResources))
	r := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.Input](), reflect.TypeFor[read.Output]())
	w := payloadArtifact(t, resources, reflect.TypeFor[write.WriterComponent](), reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output]())
	reader, err := r.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: w.ViewDependencies, Input: w.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(w.Component, reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output](), "patch")
	must(t, err)
	access := ordinaryAccess("reportaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return internal, true, nil })
	visibility := provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil })
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: r.Component, Input: r.Input, Output: r.Output, OutputType: reflect.TypeFor[read.Output](), Reader: reader, Providers: []locator.Provider{access, visibility}},
		{Component: w.Component, Input: w.Input, Output: w.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{access, visibility, views}, DataSource: dml.Source{DB: db, Tx: supplied}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, r.Component.Key, w.Component.Key
}

func invokeReportRunWriter(ctx context.Context, rt *druntime.Runtime, key spec.Key, input *write.Input) error {
	_, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/run"}}, Input: input})
	return err
}

func reportRunRow(id string, expected int64) *write.Run {
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	row := &write.Run{}
	row.SetReportRunId(id)
	row.SetOwnerId("u1")
	row.SetMaterializer("test")
	row.SetStartedAt(started)
	row.SetRevision(expected)
	row.SetCreatedAt(started)
	row.SetUpdatedAt(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC))
	row.SetReportSpecJson([]byte("{}"))
	switch id {
	case "manual":
		origin := "manual"
		completed := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		row.SetOrigin(&origin)
		row.SetStatus("completed")
		row.SetCompletedAt(&completed)
		row.SetUiRunRequestId("req-manual")
	case "running":
		origin := "interactive"
		row.SetOrigin(&origin)
		row.SetStatus("running")
		row.SetUiRunRequestId("req-running")
	default:
		row.SetStatus("running")
		row.SetUiRunRequestId("req-" + id)
	}
	return row
}

func TestReportRunWriterModes(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		mode, id          string
		expected, desired int64
		mutateSnapshot    bool
	}
	type expect struct {
		failure      bool
		class        string
		revision     int64
		conversation string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"create new run", input{mode: "create", id: "new", expected: 1}, expect{revision: 1}},
		{"duplicate create fails", input{mode: "create", id: "running", expected: 1}, expect{failure: true, class: "duplicate", revision: 1}},
		{"update running exact revision", input{mode: "update", id: "running", expected: 1, desired: 2}, expect{revision: 2}},
		{"update stale revision fails", input{mode: "update", id: "running", expected: 9, desired: 10}, expect{failure: true, class: "cas", revision: 1}},
		{"update missing run is not found", input{mode: "update", id: "missing", expected: 1, desired: 2}, expect{failure: true, class: "notfound"}},
		{"ordinary update cannot mutate completed snapshot", input{mode: "update", id: "manual", expected: 2, desired: 3}, expect{failure: true, class: "immutable", revision: 2}},
		{"adopt completed manual snapshot", input{mode: "adopt", id: "manual", expected: 2, desired: 3}, expect{revision: 3, conversation: "c1"}},
		{"adoption cannot modify snapshot fields", input{mode: "adopt", id: "manual", expected: 2, desired: 3, mutateSnapshot: true}, expect{failure: true, revision: 2}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			var oldDB *sql.DB
			var oldPath string
			compareLegacy := tc.input.mode != "adopt"
			if compareLegacy {
				oldDB, oldPath = reportRunFixture(t, project)
			}
			db, _ := reportRunFixture(t, project)
			var adapterDB *sql.DB
			var adapter *runstore.Store
			if compareLegacy {
				adapterDB, _ = reportRunFixture(t, project)
				adapterRT, _, _ := reportRunRuntime(t, adapterDB, "u1", false, nil)
				adapter = &runstore.Store{Invoker: adapterRT, OwnerID: func(context.Context) string { return "u1" }}
			}
			rt, _, key := reportRunRuntime(t, db, "u1", false, nil)
			row := reportRunRow(tc.input.id, tc.input.expected)
			if tc.input.mode == "adopt" {
				conversation, source, actor := "c1", "manual-adopt", "u1"
				row.SetConversationId(&conversation)
				row.SetAdoptionSource(&source)
				row.SetActorId(&actor)
			}
			if tc.input.mutateSnapshot {
				row.SetReportSpecJson([]byte(`{"changed":true}`))
			}
			input := &write.Input{}
			input.SetMode(tc.input.mode)
			input.SetRuns([]*write.Run{row})
			if tc.input.mode != "create" {
				input.SetDesiredRevision(tc.input.desired)
				input.SetExpectedRequestID(row.UiRunRequestId)
			}
			err := invokeReportRunWriter(context.Background(), rt, key, input)
			if (err != nil) != tc.expect.failure {
				t.Fatalf("writer error=%v expected failure=%v", err, tc.expect.failure)
			}
			if adapter != nil {
				origin := "interactive"
				status := "running"
				if tc.input.id == "manual" {
					origin, status = "manual", "completed"
				}
				record := &runstore.Record{
					ReportRunID: tc.input.id, OwnerID: "u1", Materializer: "test", Origin: origin,
					Status: status, StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
					Revision: tc.input.expected, UIRunRequestID: row.UiRunRequestId, ReportSpec: json.RawMessage(`{}`),
					CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
					UpdatedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
				}
				if tc.input.mode == "update" {
					record.Revision = tc.input.desired
				}
				var adapterErr error
				if tc.input.mode == "create" {
					adapterErr = adapter.Create(context.Background(), record)
				} else {
					adapterErr = adapter.UpdateCAS(context.Background(), record, tc.input.expected)
				}
				if (adapterErr != nil) != tc.expect.failure {
					t.Fatalf("adapter error=%v expected failure=%v", adapterErr, tc.expect.failure)
				}
				switch tc.expect.class {
				case "duplicate":
					if !errors.Is(adapterErr, runstore.ErrAlreadyExists) {
						t.Fatalf("adapter duplicate=%v", adapterErr)
					}
				case "cas":
					if !errors.Is(adapterErr, runstore.ErrCASMismatch) {
						t.Fatalf("adapter CAS=%v", adapterErr)
					}
				case "notfound":
					if !errors.Is(adapterErr, runstore.ErrNotFound) {
						t.Fatalf("adapter notfound=%v", adapterErr)
					}
				case "immutable":
					if !errors.Is(adapterErr, runstore.ErrImmutable) {
						t.Fatalf("adapter immutable=%v", adapterErr)
					}
				}
			}
			if compareLegacy {
				legacyBody, e := json.Marshal(map[string]any{
					"reportRunId": tc.input.id, "ownerId": "u1", "materializer": "test",
					"origin":    map[bool]string{true: "manual", false: "interactive"}[tc.input.id == "manual"],
					"status":    map[bool]string{true: "completed", false: "running"}[tc.input.id == "manual"],
					"startedAt": "2026-01-01T00:00:00Z", "revision": map[bool]int64{true: tc.input.desired, false: tc.input.expected}[tc.input.mode == "update"],
					"uiRunRequestId": row.UiRunRequestId, "reportSpec": json.RawMessage(`{}`),
					"createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-03T00:00:00Z",
				})
				must(t, e)
				payload, e := json.Marshal(map[string]any{
					"Component": "reportRun", "DBPath": oldPath, "Principal": "u1",
					"Method": tc.input.mode, "Body": string(legacyBody), "ExpectedRevision": tc.input.expected,
				})
				must(t, e)
				cmd := exec.Command(legacy)
				cmd.Stdin = bytes.NewReader(payload)
				raw, e := cmd.Output()
				must(t, e)
				var before probeResult
				must(t, json.Unmarshal(raw, &before))
				if before.Failed != tc.expect.failure {
					t.Fatalf("legacy failure=%v (%s), native=%v", before.Failed, before.Error, err)
				}
			}
			var revision int64
			var conversation string
			err = db.QueryRow("SELECT revision,COALESCE(conversation_id,'') FROM report_run WHERE report_run_id=?", tc.input.id).Scan(&revision, &conversation)
			if tc.input.id == "missing" && tc.expect.failure {
				if err != sql.ErrNoRows {
					t.Fatalf("unexpected new run revision=%d err=%v", revision, err)
				}
				return
			}
			must(t, err)
			if revision != tc.expect.revision || conversation != tc.expect.conversation {
				t.Fatalf("stored revision/conversation=(%d,%q), expected=(%d,%q)", revision, conversation, tc.expect.revision, tc.expect.conversation)
			}
			if compareLegacy {
				var oldRevision int64
				var oldConversation string
				must(t, oldDB.QueryRow("SELECT revision,COALESCE(conversation_id,'') FROM report_run WHERE report_run_id=?", tc.input.id).Scan(&oldRevision, &oldConversation))
				var adapterRevision int64
				var adapterConversation string
				must(t, adapterDB.QueryRow("SELECT revision,COALESCE(conversation_id,'') FROM report_run WHERE report_run_id=?", tc.input.id).Scan(&adapterRevision, &adapterConversation))
				if oldRevision != revision || oldConversation != conversation {
					t.Fatalf("legacy revision/conversation=(%d,%q), native=(%d,%q)", oldRevision, oldConversation, revision, conversation)
				}
				if adapterRevision != revision || adapterConversation != conversation {
					t.Fatalf("adapter revision/conversation=(%d,%q), native=(%d,%q)", adapterRevision, adapterConversation, revision, conversation)
				}
			}
		})
	}
}

func reportRunReaderFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := reportRunFixture(t, project)
	_, err := db.Exec(`UPDATE report_run SET conversation_id='c1', builder_ref='builder', preset_id='preset',
 source_kind='dataset', source_id='source', requested_params_json=X'7B2261223A317D',
 effective_params_json=X'7B2262223A327D', failure_code='code', failure_text='text',
 report_fill_json=X'7B2263223A337D', report_print_json=X'7B2264223A347D',
 activation_source='source-a', adoption_source='source-b', actor_id='u1'
 WHERE report_run_id='running'`)
	must(t, err)
	return db, path
}

type reportRunSnapshot struct {
	ReportRunID      string          `json:"reportRunId"`
	OwnerID          string          `json:"ownerId"`
	ConversationID   string          `json:"conversationId,omitempty"`
	Materializer     string          `json:"materializer"`
	Origin           string          `json:"origin,omitempty"`
	BuilderRef       string          `json:"builderRef,omitempty"`
	PresetID         string          `json:"presetId,omitempty"`
	SourceKind       string          `json:"sourceKind,omitempty"`
	SourceID         string          `json:"sourceId,omitempty"`
	RequestedParams  json.RawMessage `json:"requestedParams,omitempty"`
	EffectiveParams  json.RawMessage `json:"effectiveParams,omitempty"`
	Status           string          `json:"status"`
	FailureCode      string          `json:"failureCode,omitempty"`
	FailureText      string          `json:"failureText,omitempty"`
	StartedAt        time.Time       `json:"startedAt"`
	CompletedAt      *time.Time      `json:"completedAt,omitempty"`
	Revision         int64           `json:"revision"`
	UIRunRequestID   string          `json:"uiRunRequestId"`
	ReportSpec       json.RawMessage `json:"reportSpec,omitempty"`
	ReportFill       json.RawMessage `json:"reportFill,omitempty"`
	ReportPrint      json.RawMessage `json:"reportPrint,omitempty"`
	ActivationSource string          `json:"activationSource,omitempty"`
	AdoptionSource   string          `json:"adoptionSource,omitempty"`
	ActorID          string          `json:"actorId,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}

func reportRunSnapshotFromView(row *read.Run) reportRunSnapshot {
	return reportRunSnapshot{
		ReportRunID: row.ReportRunId, OwnerID: row.OwnerId, ConversationID: row.ConversationId,
		Materializer: row.Materializer, Origin: row.Origin, BuilderRef: row.BuilderRef,
		PresetID: row.PresetId, SourceKind: row.SourceKind, SourceID: row.SourceId,
		RequestedParams: json.RawMessage(row.RequestedParamsJson), EffectiveParams: json.RawMessage(row.EffectiveParamsJson),
		Status: row.Status, FailureCode: row.FailureCode, FailureText: row.FailureText,
		StartedAt: row.StartedAt, CompletedAt: row.CompletedAt, Revision: row.Revision,
		UIRunRequestID: row.UiRunRequestId, ReportSpec: json.RawMessage(row.ReportSpecJson),
		ReportFill: json.RawMessage(row.ReportFillJson), ReportPrint: json.RawMessage(row.ReportPrintJson),
		ActivationSource: row.ActivationSource, AdoptionSource: row.AdoptionSource, ActorID: row.ActorId,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func reportRunSnapshotFromAdapter(row *runstore.Record) reportRunSnapshot {
	return reportRunSnapshot{
		ReportRunID: row.ReportRunID, OwnerID: row.OwnerID, ConversationID: row.ConversationID,
		Materializer: row.Materializer, Origin: row.Origin, BuilderRef: row.BuilderRef,
		PresetID: row.PresetID, SourceKind: row.SourceKind, SourceID: row.SourceID,
		RequestedParams: row.RequestedParams, EffectiveParams: row.EffectiveParams,
		Status: row.Status, FailureCode: row.FailureCode, FailureText: row.FailureText,
		StartedAt: row.StartedAt, CompletedAt: row.CompletedAt, Revision: row.Revision,
		UIRunRequestID: row.UIRunRequestID, ReportSpec: row.ReportSpec,
		ReportFill: row.ReportFill, ReportPrint: row.ReportPrint,
		ActivationSource: row.ActivationSource, AdoptionSource: row.AdoptionSource, ActorID: row.ActorID,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func TestReportRunReaderLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		subject, id, requestID, method string
		internal                       bool
	}
	type expect struct {
		id            string
		compareLegacy bool
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"exact ID includes all physical fields", input{subject: "u1", id: "running", method: "get"}, expect{id: "running", compareLegacy: true}},
		{"request identity resolves manual run", input{subject: "u1", requestID: "req-manual", method: "getByRequest"}, expect{id: "manual", compareLegacy: true}},
		{"foreign owner cannot read run", input{subject: "u2", id: "running", method: "get"}, expect{compareLegacy: true}},
		{"trusted internal can read foreign run", input{internal: true, id: "foreign"}, expect{id: "foreign"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := reportRunReaderFixture(t, project)
			db, _ := reportRunReaderFixture(t, project)
			rt, key, _ := reportRunRuntime(t, db, tc.input.subject, tc.input.internal, nil)
			query := &read.Input{}
			if tc.input.id != "" {
				query.SetReportRunID(tc.input.id)
			}
			if tc.input.requestID != "" {
				query.SetUIRunRequestID(tc.input.requestID)
			}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/run"}}, Input: query})
			must(t, err)
			rows := value.(*read.Output).Data
			if tc.expect.id == "" {
				if len(rows) != 0 {
					t.Fatalf("foreign run leaked: %+v", rows)
				}
			} else if len(rows) != 1 || rows[0].ReportRunId != tc.expect.id {
				t.Fatalf("rows=%+v, want %q", rows, tc.expect.id)
			}
			if !tc.expect.compareLegacy {
				return
			}
			store := &runstore.Store{Invoker: rt, OwnerID: func(context.Context) string { return tc.input.subject }}
			var adapterRow *runstore.Record
			if tc.input.method == "getByRequest" {
				adapterRow, err = store.GetByRequestID(context.Background(), tc.input.requestID)
			} else {
				adapterRow, err = store.Get(context.Background(), tc.input.id)
			}
			if tc.expect.id == "" {
				if !errors.Is(err, runstore.ErrNotFound) {
					t.Fatalf("adapter foreign read error=%v", err)
				}
			} else if err != nil || adapterRow == nil {
				t.Fatalf("adapter read=(%+v,%v)", adapterRow, err)
			}
			body, e := json.Marshal(map[string]any{"reportRunId": tc.input.id, "uiRunRequestId": tc.input.requestID})
			must(t, e)
			payload, e := json.Marshal(map[string]any{"Component": "reportRun", "DBPath": oldPath, "Principal": tc.input.subject, "Method": tc.input.method, "Body": string(body)})
			must(t, e)
			cmd := exec.Command(legacy)
			cmd.Stdin = bytes.NewReader(payload)
			raw, e := cmd.Output()
			must(t, e)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed != (tc.expect.id == "") {
				t.Fatalf("legacy failed=%v (%s), native rows=%d", before.Failed, before.Error, len(rows))
			}
			if tc.expect.id == "" {
				return
			}
			nativeJSON, e := json.Marshal(reportRunSnapshotFromView(rows[0]))
			must(t, e)
			var oldFields, newFields map[string]any
			must(t, json.Unmarshal(before.Output, &oldFields))
			must(t, json.Unmarshal(nativeJSON, &newFields))
			if !reflect.DeepEqual(oldFields, newFields) {
				t.Fatalf("legacy=%s native=%s", before.Output, nativeJSON)
			}
			adapterJSON, e := json.Marshal(reportRunSnapshotFromAdapter(adapterRow))
			must(t, e)
			var adapterFields map[string]any
			must(t, json.Unmarshal(adapterJSON, &adapterFields))
			if !reflect.DeepEqual(oldFields, adapterFields) {
				t.Fatalf("legacy=%s adapter=%s", before.Output, adapterJSON)
			}
		})
	}
}

func TestReportRunCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		commit bool
		expect int64
	}
	for _, tc := range []useCase{{"caller rollback", false, 1}, {"caller commit", true, 2}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportRunFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _, key := reportRunRuntime(t, db, "u1", false, tx)
			input := &write.Input{}
			input.SetMode("update")
			input.SetDesiredRevision(2)
			input.SetExpectedRequestID("req-running")
			input.SetRuns([]*write.Run{reportRunRow("running", 1)})
			must(t, invokeReportRunWriter(context.Background(), rt, key, input))
			var pending int64
			must(t, tx.QueryRow("SELECT revision FROM report_run WHERE report_run_id='running'").Scan(&pending))
			if pending != 2 {
				t.Fatalf("pending revision=%d", pending)
			}
			if tc.commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var stored int64
			must(t, db.QueryRow("SELECT revision FROM report_run WHERE report_run_id='running'").Scan(&stored))
			if stored != tc.expect {
				t.Fatalf("stored revision=%d expected=%d", stored, tc.expect)
			}
		})
	}
}

func TestReportRunIndependentConnectionsCAS(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, path := reportRunFixture(t, project)
	_, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000")
	must(t, err)
	other, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	must(t, err)
	defer other.Close()
	other.SetMaxOpenConns(1)
	_, err = other.Exec("PRAGMA busy_timeout=5000")
	must(t, err)
	first, _, firstKey := reportRunRuntime(t, db, "u1", false, nil)
	second, _, secondKey := reportRunRuntime(t, other, "u1", false, nil)
	runtimes := []*druntime.Runtime{first, second}
	keys := []spec.Key{firstKey, secondKey}
	start := make(chan struct{})
	results := make([]error, 2)
	var group sync.WaitGroup
	for i := range results {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			input := &write.Input{}
			input.SetMode("update")
			input.SetDesiredRevision(2)
			input.SetExpectedRequestID("req-running")
			input.SetRuns([]*write.Run{reportRunRow("running", 1)})
			results[index] = invokeReportRunWriter(context.Background(), runtimes[index], keys[index], input)
		}(i)
	}
	close(start)
	group.Wait()
	success := 0
	for _, result := range results {
		if result == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("CAS successes=%d errors=%v", success, results)
	}
	var revision int64
	must(t, db.QueryRow("SELECT revision FROM report_run WHERE report_run_id='running'").Scan(&revision))
	if revision != 2 {
		t.Fatalf("final revision=%d", revision)
	}
}

func TestReportRunDeleteRevisionGuard(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject  string
		revision int64
	}
	type expect struct {
		failure   bool
		remaining int
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"exact revision deletes", input{subject: "u1", revision: 1}, expect{remaining: 0}},
		{"stale revision fails", input{subject: "u1", revision: 9}, expect{failure: true, remaining: 1}},
		{"foreign owner fails", input{subject: "u2", revision: 1}, expect{failure: true, remaining: 1}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportRunFixture(t, project)
			rt, _, key := reportRunRuntime(t, db, tc.input.subject, false, nil)
			row := &write.Run{}
			row.SetReportRunId("running")
			row.SetOwnerId("u1")
			row.SetRevision(tc.input.revision)
			row.SetShouldDelete(true)
			input := &write.Input{}
			input.SetMode("delete")
			input.SetRuns([]*write.Run{row})
			err := invokeReportRunWriter(context.Background(), rt, key, input)
			if (err != nil) != tc.expect.failure {
				t.Fatalf("delete error=%v expected failure=%v", err, tc.expect.failure)
			}
			var remaining int
			must(t, db.QueryRow("SELECT COUNT(*) FROM report_run WHERE report_run_id='running'").Scan(&remaining))
			if remaining != tc.expect.remaining {
				t.Fatalf("remaining=%d expected=%d", remaining, tc.expect.remaining)
			}
		})
	}
}

func TestReportRunHTTPCannotOverrideOwner(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := reportRunFixture(t, project)
	rt, _, _ := reportRunRuntime(t, db, "u2", false, nil)
	request := httptest.NewRequest("GET", "/v1/internal/forge/reporting/run?reportRunId=running&internal=true&ownerSubject=u1", nil)
	scope, err := requestprovider.New(request)
	must(t, err)
	defer scope.Close()
	value, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/forge/reporting/run", scope)
	must(t, err)
	if len(value.(*read.Output).Data) != 0 {
		t.Fatal("HTTP query replaced host owner scope")
	}
}
