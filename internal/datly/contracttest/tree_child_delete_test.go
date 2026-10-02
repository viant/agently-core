package tests

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	filewrite "github.com/viant/agently-core/internal/datly/generatedfile/write"
	queuewrite "github.com/viant/agently-core/internal/datly/turnqueue/write"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

func TestGeneratedFileTreeDeleteBatch(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(source))))
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "idempotent delete", true: "late failure rollback"}[fail], func(t *testing.T) {
			db, _ := generatedFileFixture(t, project)
			if fail {
				_, err := db.Exec(`CREATE TRIGGER reject_file_delete BEFORE DELETE ON generated_file WHEN OLD.id='f3' BEGIN SELECT RAISE(ABORT,'late file deletion failure'); END`)
				must(t, err)
			}
			rt, _ := generatedFileRuntime(t, db)
			input := &filewrite.Input{}
			rows := []*filewrite.GeneratedFile{}
			for _, id := range []string{"missing", "f1", "f3"} {
				row := &filewrite.GeneratedFile{}
				row.SetId(id)
				row.SetShouldDelete(true)
				rows = append(rows, row)
			}
			input.SetGeneratedFiles(rows)
			request := dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[filewrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/generated-file"}}, Input: input}
			_, err := rt.InvokeComponent(context.Background(), request)
			if fail {
				if err == nil || !strings.Contains(err.Error(), "late file deletion failure") {
					t.Fatalf("expected late failure, got %v", err)
				}
			} else {
				must(t, err)
				_, err = rt.InvokeComponent(context.Background(), request)
				must(t, err)
			}
			var remaining int
			must(t, db.QueryRow(`SELECT COUNT(*) FROM generated_file WHERE id IN ('f1','f3')`).Scan(&remaining))
			expected := 0
			if fail {
				expected = 2
			}
			if remaining != expected {
				t.Fatalf("remaining=%d expected=%d", remaining, expected)
			}
			var other int
			must(t, db.QueryRow(`SELECT COUNT(*) FROM generated_file WHERE id='f2'`).Scan(&other))
			if other != 1 {
				t.Fatal("deleted unrelated file")
			}
		})
	}
}

func TestTurnQueueTreeDeleteBatch(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(source))))
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "idempotent delete", true: "late failure rollback"}[fail], func(t *testing.T) {
			db, _ := queueParityFixture(t, project)
			if fail {
				_, err := db.Exec(`CREATE TRIGGER reject_queue_delete BEFORE DELETE ON turn_queue WHEN OLD.id='q2' BEGIN SELECT RAISE(ABORT,'late queue deletion failure'); END`)
				must(t, err)
			}
			rt, _ := queueParityRuntime(t, db)
			input := &queuewrite.Input{}
			rows := []*queuewrite.TurnQueue{}
			for _, id := range []string{"missing", "q1", "q2"} {
				row := &queuewrite.TurnQueue{}
				row.SetId(id)
				row.SetShouldDelete(true)
				rows = append(rows, row)
			}
			input.SetQueues(rows)
			request := dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[queuewrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/turnqueue"}}, Input: input}
			_, err := rt.InvokeComponent(context.Background(), request)
			if fail {
				if err == nil || !strings.Contains(err.Error(), "late queue deletion failure") {
					t.Fatalf("expected late failure, got %v", err)
				}
			} else {
				must(t, err)
				_, err = rt.InvokeComponent(context.Background(), request)
				must(t, err)
			}
			var remaining int
			must(t, db.QueryRow(`SELECT COUNT(*) FROM turn_queue WHERE id IN ('q1','q2')`).Scan(&remaining))
			expected := 0
			if fail {
				expected = 2
			}
			if remaining != expected {
				t.Fatalf("remaining=%d expected=%d", remaining, expected)
			}
		})
	}
}
