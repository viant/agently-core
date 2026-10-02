package tests

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"

	turnwrite "github.com/viant/agently-core/internal/datly/turn/write"
	queueread "github.com/viant/agently-core/internal/datly/turnqueue/read"
	queuewrite "github.com/viant/agently-core/internal/datly/turnqueue/write"
	reorder "github.com/viant/agently-core/internal/store/queuereorder"
	"github.com/viant/bindly/locator"
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
	dtag "github.com/viant/datly/tag"
)

func TestQueueReorderHostRoutePrivate(t *testing.T) {
	holder := reflect.TypeFor[reorder.ReorderComponent]()
	field, ok := holder.FieldByName("Contract")
	if !ok {
		t.Fatal("reorder contract is missing")
	}
	tag, present, err := dtag.ParseComponent(field.Tag)
	must(t, err)
	if !present {
		t.Fatal("reorder component tag is missing")
	}
	source := &bootstrap.RouteSource{HolderType: holder.Name(), FieldName: field.Name, PackageName: "queuereorder", PackagePath: holder.PkgPath(), Tag: tag, InputType: "Input", OutputType: "Output"}
	component, err := source.Resolve(reflect.TypeFor[reorder.Input](), reflect.TypeFor[reorder.Output]())
	must(t, err)
	if len(component.Routes) != 1 || !component.Routes[0].Internal {
		t.Fatalf("reorder route is public: %+v", component.Routes)
	}
}

func TestQueueReorderCallerMove(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	for _, tc := range []struct {
		name, turnID, direction string
		wantFirst, wantSecond   int64
		wantError               error
	}{
		{name: "move up", turnID: "t1", direction: "up", wantFirst: 1, wantSecond: 2},
		{name: "move down", turnID: "t2", direction: "down", wantFirst: 1, wantSecond: 2},
		{name: "cannot move past edge", turnID: "t2", direction: "up", wantFirst: 2, wantSecond: 1, wantError: reorder.ErrMoveOutsideQueue},
		{name: "unknown turn", turnID: "missing", direction: "up", wantFirst: 2, wantSecond: 1, wantError: reorder.ErrTurnNotQueued},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := queueParityFixture(t, project)
			_, err := db.Exec("UPDATE turn SET queue_seq=2 WHERE id='t1'; UPDATE turn SET queue_seq=1 WHERE id='t2'; UPDATE turn_queue SET status='queued' WHERE id='q2'")
			must(t, err)
			rt, _ := queueReorderRuntime(t, db, nil)
			err = reorder.Move(context.Background(), rt, "c1", tc.turnID, tc.direction)
			if !errors.Is(err, tc.wantError) || (tc.wantError == nil && err != nil) {
				t.Fatalf("Move returned %v, wanted %v", err, tc.wantError)
			}
			first, second := queueReorderSequences(t, db)
			if first != tc.wantFirst || second != tc.wantSecond {
				t.Fatalf("queue order=(%d,%d), want=(%d,%d)", first, second, tc.wantFirst, tc.wantSecond)
			}
		})
	}
}

func queueReorderRuntime(t *testing.T, db *sql.DB, supplied *sql.Tx) (*druntime.Runtime, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(queueread.ReaderDatlyResourceNamespace, queueread.ReaderDatlyResources))
	must(t, resources.Register(turnwrite.WriterDatlyResourceNamespace, turnwrite.WriterDatlyResources))
	must(t, resources.Register(queuewrite.WriterDatlyResourceNamespace, queuewrite.WriterDatlyResources))
	readerArtifact := payloadArtifact(t, resources, reflect.TypeFor[queueread.ReaderComponent](), reflect.TypeFor[queueread.QueueRowsInput](), reflect.TypeFor[queueread.QueueRowsOutput]())
	turnArtifact := payloadArtifact(t, resources, reflect.TypeFor[turnwrite.WriterComponent](), reflect.TypeFor[turnwrite.Input](), reflect.TypeFor[turnwrite.Output]())
	queueArtifact := payloadArtifact(t, resources, reflect.TypeFor[queuewrite.WriterComponent](), reflect.TypeFor[queuewrite.Input](), reflect.TypeFor[queuewrite.Output]())
	parentArtifact := payloadArtifact(t, resources, reflect.TypeFor[reorder.ReorderComponent](), reflect.TypeFor[reorder.Input](), reflect.TypeFor[reorder.Output]())
	handler, err := (reorder.ReorderComponent{}).DatlyHandler("NewQueueReorder")()
	must(t, err)
	builder, err := bootstrap.NewArtifactBuilder(nil)
	must(t, err)
	parentArtifact, err = builder.Build(bootstrap.ArtifactInput{Component: parentArtifact.Component, InputType: reflect.TypeFor[reorder.Input](), OutputType: reflect.TypeFor[reorder.Output](), Resources: resources, Handler: handler})
	must(t, err)
	parentRegistration, err := parentArtifact.Registration(registry.RegisteredComponent{DataSource: dml.Source{DB: db, Tx: supplied}})
	must(t, err)
	reader, err := readerArtifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	turnViews, err := viewprovider.New(viewprovider.Config{Dependencies: turnArtifact.ViewDependencies, Input: turnArtifact.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	queueViews, err := viewprovider.New(viewprovider.Config{Dependencies: queueArtifact.ViewDependencies, Input: queueArtifact.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	turnWriter, err := writer.New(turnArtifact.Component, reflect.TypeFor[turnwrite.Input](), reflect.TypeFor[turnwrite.Output](), "patch")
	must(t, err)
	queueWriter, err := writer.New(queueArtifact.Component, reflect.TypeFor[queuewrite.Input](), reflect.TypeFor[queuewrite.Output](), "patch")
	must(t, err)
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: readerArtifact.Component, Input: readerArtifact.Input, Output: readerArtifact.Output, OutputType: reflect.TypeFor[queueread.QueueRowsOutput](), Reader: reader},
		{Component: turnArtifact.Component, Input: turnArtifact.Input, Output: turnArtifact.Output, OutputType: reflect.TypeFor[turnwrite.Output](), Handler: turnWriter, Providers: []locator.Provider{turnViews}, DataSource: dml.Source{DB: db}},
		{Component: queueArtifact.Component, Input: queueArtifact.Input, Output: queueArtifact.Output, OutputType: reflect.TypeFor[queuewrite.Output](), Handler: queueWriter, Providers: []locator.Provider{queueViews}, DataSource: dml.Source{DB: db}},
		parentRegistration,
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, parentArtifact.Component.Key
}

func TestQueueReorderIndependentConnections(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, path := queueParityFixture(t, project)
	_, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; UPDATE turn SET queue_seq=2 WHERE id='t1'; UPDATE turn SET queue_seq=1 WHERE id='t2'; UPDATE turn_queue SET status='queued' WHERE id='q2'")
	must(t, err)
	other, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	must(t, err)
	defer other.Close()
	other.SetMaxOpenConns(1)
	_, err = other.Exec("PRAGMA busy_timeout=5000")
	must(t, err)
	first, firstKey := queueReorderRuntime(t, db, nil)
	second, secondKey := queueReorderRuntime(t, other, nil)
	type useCase struct {
		desc  string
		input *reorder.Input
	}
	useCases := []useCase{
		{"first independent caller", &reorder.Input{ConversationID: "c1", FirstID: "t1", SecondID: "t2", FirstSequence: 2, SecondSequence: 1}},
		{"second independent caller", &reorder.Input{ConversationID: "c1", FirstID: "t1", SecondID: "t2", FirstSequence: 2, SecondSequence: 1}},
	}
	runtimes := []*druntime.Runtime{first, second}
	keys := []spec.Key{firstKey, secondKey}
	start := make(chan struct{})
	results := make([]error, len(useCases))
	var group sync.WaitGroup
	for i, tc := range useCases {
		group.Add(1)
		go func(index int, item useCase) {
			defer group.Done()
			<-start
			_, results[index] = runtimes[index].InvokeComponent(context.Background(), dexec.ComponentRequest{
				Target: dexec.ComponentTarget{Component: keys[index], Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/turnqueue/reorder"}},
				Input:  item.input,
			})
		}(i, tc)
	}
	close(start)
	group.Wait()
	success := 0
	for _, err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("successes=%d, errors=%v", success, results)
	}
	firstSeq, secondSeq := queueReorderSequences(t, db)
	if firstSeq != 1 || secondSeq != 2 {
		t.Fatalf("concurrent final order=(%d,%d)", firstSeq, secondSeq)
	}
}

func TestQueueReorderSharedTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		conversation string
		firstSeq     int64
		rejectSecond bool
		callerTx     bool
		commit       bool
	}
	type expect struct {
		failure       bool
		first, second int64
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"managed commit changes turn and queue together", input{conversation: "c1", firstSeq: 2}, expect{first: 1, second: 2}},
		{"zero sequence is a supplied expected value", input{conversation: "c1", firstSeq: 0}, expect{first: 1, second: 0}},
		{"late queue failure rolls back prior turn writes", input{conversation: "c1", firstSeq: 2, rejectSecond: true}, expect{failure: true, first: 2, second: 1}},
		{"stale sequence is rejected before mutation", input{conversation: "c1", firstSeq: 99}, expect{failure: true, first: 2, second: 1}},
		{"wrong conversation is rejected", input{conversation: "other", firstSeq: 2}, expect{failure: true, first: 2, second: 1}},
		{"caller transaction rollback retains ownership", input{conversation: "c1", firstSeq: 2, callerTx: true}, expect{first: 2, second: 1}},
		{"caller transaction commit retains ownership", input{conversation: "c1", firstSeq: 2, callerTx: true, commit: true}, expect{first: 1, second: 2}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := queueParityFixture(t, project)
			_, err := db.Exec("UPDATE turn SET queue_seq=2 WHERE id='t1'; UPDATE turn SET queue_seq=1 WHERE id='t2'; UPDATE turn_queue SET status='queued' WHERE id='q2'")
			must(t, err)
			if tc.input.firstSeq == 0 {
				_, err = db.Exec("UPDATE turn SET queue_seq=0 WHERE id='t1'; UPDATE turn_queue SET queue_seq=0 WHERE id='q1'")
				must(t, err)
			}
			if tc.input.rejectSecond {
				_, err = db.Exec("CREATE TRIGGER reject_second_queue_update BEFORE UPDATE ON turn_queue WHEN OLD.id='q2' BEGIN SELECT RAISE(ABORT,'fixture second queue rejection'); END")
				must(t, err)
			}
			db.SetMaxOpenConns(1)
			var tx *sql.Tx
			if tc.input.callerTx {
				tx, err = db.BeginTx(context.Background(), nil)
				must(t, err)
				defer tx.Rollback()
			}
			rt, key := queueReorderRuntime(t, db, tx)
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{
				Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/turnqueue/reorder"}},
				Input:  &reorder.Input{ConversationID: tc.input.conversation, FirstID: "t1", SecondID: "t2", FirstSequence: tc.input.firstSeq, SecondSequence: 1},
			})
			if (err != nil) != tc.expect.failure {
				t.Fatalf("reorder result=%T err=%v, expected failure=%v", value, err, tc.expect.failure)
			}
			if tc.input.firstSeq == 99 || tc.input.conversation == "other" {
				if !errors.Is(err, reorder.ErrConflict) {
					t.Fatalf("stale or foreign selection returned %v", err)
				}
			}
			if err == nil && (value.(*reorder.Output).FirstSequence != 1 || value.(*reorder.Output).SecondSequence != tc.input.firstSeq) {
				t.Fatalf("reorder output=%+v", value)
			}
			if tx != nil {
				first, second := queueReorderSequences(t, tx)
				if first != 1 || second != 2 {
					t.Fatalf("caller transaction pending sequence=(%d,%d)", first, second)
				}
				if tc.input.commit {
					must(t, tx.Commit())
				} else {
					must(t, tx.Rollback())
				}
			}
			first, second := queueReorderSequences(t, db)
			if first != tc.expect.first || second != tc.expect.second {
				t.Fatalf("stored sequence=(%d,%d), expected=(%d,%d)", first, second, tc.expect.first, tc.expect.second)
			}
		})
	}
}

type queueSequenceQuery interface {
	QueryRow(query string, args ...any) *sql.Row
}

func queueReorderSequences(t *testing.T, db queueSequenceQuery) (int64, int64) {
	t.Helper()
	turnFirst, turnSecond, queueFirst, queueSecond := queueRawSequences(t, db)
	if turnFirst != queueFirst || turnSecond != queueSecond {
		t.Fatalf("turn/queue sequence mismatch: turn=(%d,%d), queue=(%d,%d)", turnFirst, turnSecond, queueFirst, queueSecond)
	}
	return queueFirst, queueSecond
}

func queueRawSequences(t *testing.T, db queueSequenceQuery) (int64, int64, int64, int64) {
	t.Helper()
	var turnFirst, turnSecond, queueFirst, queueSecond int64
	must(t, db.QueryRow("SELECT queue_seq FROM turn WHERE id='t1'").Scan(&turnFirst))
	must(t, db.QueryRow("SELECT queue_seq FROM turn WHERE id='t2'").Scan(&turnSecond))
	must(t, db.QueryRow("SELECT queue_seq FROM turn_queue WHERE id='q1'").Scan(&queueFirst))
	must(t, db.QueryRow("SELECT queue_seq FROM turn_queue WHERE id='q2'").Scan(&queueSecond))
	return turnFirst, turnSecond, queueFirst, queueSecond
}
