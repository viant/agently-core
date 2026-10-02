package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	base "github.com/viant/agently-core/internal/datly/message/base"
	read "github.com/viant/agently-core/internal/datly/message/read"
	"github.com/viant/datly/bootstrap"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	write "github.com/viant/agently-core/internal/datly/message/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	druntime "github.com/viant/datly/runtime"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
)

func TestMessageWriterLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc, input string
		expect      bool
	}
	for _, tc := range []useCase{
		{"insert allocates next sequence", `{"data":[{"id":"new","conversationId":"c1","turnId":"t1","role":"assistant","type":"text","content":"hello"}]}`, false},
		{"independent turn starts at one", `{"data":[{"id":"new","conversationId":"c1","turnId":"t2","role":"assistant","type":"text"}]}`, false},
		{"explicit null allocates on insert", `{"data":[{"id":"new","conversationId":"c1","turnId":"t1","sequence":null,"role":"assistant","type":"text"}]}`, false},
		{"explicit null clears on update", `{"data":[{"id":"existing","sequence":null}]}`, false},
		{"explicit zero sequence", `{"data":[{"id":"new","conversationId":"c1","turnId":"t1","sequence":0,"role":"assistant","type":"text"}]}`, false},
		{"explicit sequence preserved", `{"data":[{"id":"new","conversationId":"c1","turnId":"t1","sequence":9,"role":"assistant","type":"text"}]}`, false},
		{"empty turn does not allocate", `{"data":[{"id":"new","conversationId":"c1","role":"assistant","type":"text"}]}`, false},
		{"sparse update keeps business context", `{"data":[{"id":"existing","content":"updated"}]}`, false},
		{"identity-only update keeps sequence", `{"data":[{"id":"existing"}]}`, false},
		{"explicit null content and status", `{"data":[{"id":"existing","content":null,"status":null}]}`, false},
		{"explicit zero iteration and interim", `{"data":[{"id":"existing","iteration":0,"interim":0}]}`, false},
		{"explicit empty phase and mode", `{"data":[{"id":"existing","phase":"","mode":""}]}`, false},
		{"supplied creation timestamp is replaced", `{"data":[{"id":"new","conversationId":"c1","role":"assistant","type":"text","createdAt":"2020-01-01T00:00:00Z"}]}`, false},
		{"missing identity rejected", `{"data":[{"conversationId":"c1","role":"assistant","type":"text"}]}`, true},
		{"missing role rejected", `{"data":[{"id":"new","conversationId":"c1","type":"text"}]}`, true},
		{"missing type rejected", `{"data":[{"id":"new","conversationId":"c1","role":"assistant"}]}`, true},
		{"narration retains original public name", `{"data":[{"id":"existing","narration":"progress"}]}`, false},
		{"explicit null turn leaves sequence unchanged", `{"data":[{"id":"existing","turnId":null}]}`, false},
		{"explicit duplicate sequence fails", `{"data":[{"id":"new","conversationId":"c1","turnId":"t1","sequence":3,"role":"assistant","type":"text"}]}`, true},
		{"empty batch", `{"data":[]}`, false},
		{"null batch", `{"data":null}`, false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			prefix := fmt.Sprintf("message-parity-%d-", time.Now().UnixNano())
			db, _ := messageFixture(t, project, prefix)
			tc.input = strings.ReplaceAll(strings.ReplaceAll(tc.input, `"t1"`, `"`+prefix+`t1"`), `"t2"`, `"`+prefix+`t2"`)
			initial := messageStoredRows(t, db)
			rt := messageWriterRuntime(t, db)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/message", strings.NewReader(tc.input))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			value, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/message", scope)
			if (err != nil) != tc.expect {
				t.Fatalf("native error=%v expected failure=%v", err, tc.expect)
			}
			newRows := messageStoredRows(t, db)
			if tc.expect && !reflect.DeepEqual(initial, newRows) {
				t.Fatalf("failed mutation changed stored rows: before=%s after=%s", pretty(initial), pretty(newRows))
			}
			if !tc.expect {
				var submitted struct {
					Data []map[string]any `json:"data"`
				}
				must(t, json.Unmarshal([]byte(tc.input), &submitted))
				if len(value.(*write.Output).Data) != len(submitted.Data) {
					t.Fatalf("response rows=%d submitted=%d", len(value.(*write.Output).Data), len(submitted.Data))
				}
				indexed := map[string]map[string]any{}
				for _, row := range newRows {
					indexed[row["id"].(string)] = row
				}
				for _, submittedRow := range submitted.Data {
					id := submittedRow["id"].(string)
					stored := indexed[id]
					if stored == nil {
						t.Fatalf("missing persisted message %s", id)
					}
					for field, want := range submittedRow {
						if field == "createdAt" || field == "sequence" {
							continue
						}
						key := strings.ToLower(field)
						if field == "narration" {
							key = "preamble"
						}
						if got := stored[key]; !reflect.DeepEqual(got, want) {
							t.Fatalf("message %s %s=%v want %v", id, field, got, want)
						}
					}
					if id == "new" {
						wantSequence := any(nil)
						if suppliedSequence, ok := submittedRow["sequence"]; ok && suppliedSequence != nil {
							wantSequence = suppliedSequence
						} else if turn, ok := submittedRow["turnId"]; ok && turn != nil {
							wantSequence = float64(4)
							if turn == prefix+"t2" {
								wantSequence = float64(1)
							}
						}
						if stored["sequence"] != wantSequence {
							t.Fatalf("new message sequence=%v want %v", stored["sequence"], wantSequence)
						}
					} else if submittedRow["sequence"] == nil && strings.Contains(tc.input, `"sequence":null`) {
						if stored["sequence"] != nil {
							t.Fatalf("message sequence was not cleared: %v", stored["sequence"])
						}
					} else if _, supplied := submittedRow["sequence"]; !supplied && stored["sequence"] != float64(3) {
						t.Fatalf("existing sequence changed: %v", stored["sequence"])
					}
				}
			}

		})
	}
}

func messageFixture(t *testing.T, project string, prefix ...string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	turnPrefix := ""
	if len(prefix) > 0 {
		turnPrefix = prefix[0]
	}
	_, err := db.Exec("INSERT INTO turn(id,conversation_id,status) VALUES (?,'c1','running'),(?,'c1','running')", turnPrefix+"t1", turnPrefix+"t2")
	must(t, err)
	_, err = db.Exec("INSERT INTO message(id,conversation_id,turn_id,sequence,role,type,content,status,mode,phase,iteration,interim,created_at) VALUES ('existing','c1',?,3,'assistant','text','original','thinking','react','answer',2,1,'2026-01-01 00:00:00')", turnPrefix+"t1")
	must(t, err)
	return db, path
}
func messageWriterRuntime(t *testing.T, db *sql.DB, supplied ...*sql.Tx) *druntime.Runtime {
	resources := resource.New()
	must(t, resources.Register(write.WriterDatlyResourceNamespace, write.WriterDatlyResources))
	must(t, resources.Register(base.ReaderDatlyResourceNamespace, base.ReaderDatlyResources))
	canonical := payloadArtifact(t, resources, reflect.TypeFor[write.WriterComponent](), reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output]())
	var tx *sql.Tx
	if len(supplied) > 0 {
		tx = supplied[0]
	}
	sqlComponent := &dsql.SQLComponent{DB: db, Tx: tx}
	nativeHandler, err := writer.New(canonical.Component, reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output](), "patch")
	must(t, err)
	canonicalViews, err := viewprovider.New(viewprovider.Config{Dependencies: canonical.ViewDependencies, Input: canonical.Input, SQL: sqlComponent})
	must(t, err)
	facade := canonical.Component.Clone()
	facade.Name = "CoreWrite"
	facade.Key.Name = "CoreWrite"
	facade.Settings.Mutation = ""
	facade.Routes[0].Name = "CoreWrite"
	facade.Routes[0].Path = "/v1/internal/agently/message/write"
	facade.Routes[0].Handler = "CoreWrite"
	coreHandler, err := write.CoreWriterComponent{}.DatlyHandler("CoreWrite")()
	must(t, err)
	facadeArtifact, err := bootstrap.BuildArtifact(bootstrap.ArtifactInput{Component: facade, InputType: reflect.TypeFor[write.Input](), OutputType: reflect.TypeFor[write.Output](), Handler: coreHandler, HandlerOwnedOutput: true, Resources: resources})
	must(t, err)
	facadeViews, err := viewprovider.New(viewprovider.Config{Dependencies: facadeArtifact.ViewDependencies, Input: facadeArtifact.Input, SQL: sqlComponent})
	must(t, err)
	readerArtifact := payloadArtifact(t, resources, reflect.TypeFor[base.ReaderComponent](), reflect.TypeFor[read.MessagesInput](), reflect.TypeFor[base.MessagesOutput]())
	readerExecution, err := readerArtifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: sqlComponent})
	must(t, err)
	registered := []*registry.RegisteredComponent{
		{Component: canonical.Component, Input: canonical.Input, Output: canonical.Output, OutputType: reflect.TypeFor[write.Output](), Handler: nativeHandler, Providers: []locator.Provider{canonicalViews}, DataSource: dml.Source{DB: db, Tx: tx}},
		{Component: facadeArtifact.Component, Input: facadeArtifact.Input, Output: facadeArtifact.Output, OutputType: reflect.TypeFor[write.Output](), Handler: coreHandler, Providers: []locator.Provider{facadeViews}, DataSource: dml.Source{DB: db, Tx: tx}},
		{Component: readerArtifact.Component, Input: readerArtifact.Input, Output: readerArtifact.Output, OutputType: reflect.TypeFor[base.MessagesOutput](), Reader: readerExecution},
	}
	rt, err := druntime.NewRuntime(registered, druntime.WithResources(resources))
	must(t, err)
	return rt
}

func messageStoredRows(t *testing.T, db *sql.DB) []map[string]any {
	rows, err := db.Query("SELECT * FROM message ORDER BY id")
	must(t, err)
	defer rows.Close()
	columns, err := rows.Columns()
	must(t, err)
	result := []json.RawMessage{}
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		must(t, rows.Scan(targets...))
		row := map[string]any{}
		for i, name := range columns {
			value := values[i]
			if b, ok := value.([]byte); ok {
				value = string(b)
			}
			row[strings.ReplaceAll(name, "_", "")] = value
		}
		raw, err := json.Marshal(row)
		must(t, err)
		result = append(result, raw)
	}
	must(t, rows.Err())
	return normalizeRowsInOrder(t, result)
}

func TestMessageWriterCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	prefix := fmt.Sprintf("message-caller-%d-", time.Now().UnixNano())
	db, _ := messageFixture(t, project, prefix)
	for attempt, commit := range []bool{false, true} {
		name := "rollback preserves allocation gap"
		if commit {
			name = "commit retains row after gap"
		}
		t.Run(name, func(t *testing.T) {
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt := messageWriterRuntime(t, db, tx)
			body := fmt.Sprintf(`{"data":[{"id":"new","conversationId":"c1","turnId":%q,"sequence":null,"role":"assistant","type":"text"}]}`, prefix+"t1")
			request := httptest.NewRequest("PATCH", "/v1/api/agently/message", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/message", scope)
			must(t, err)
			var sequence int
			must(t, tx.QueryRow("SELECT sequence FROM message WHERE id='new'").Scan(&sequence))
			if sequence != 4+attempt {
				t.Fatalf("pending sequence=%d expected=%d", sequence, 4+attempt)
			}
			expected := 0
			if commit {
				must(t, tx.Commit())
				expected = 1
			} else {
				must(t, tx.Rollback())
			}
			var count int
			must(t, db.QueryRow("SELECT COUNT(*) FROM message WHERE id='new'").Scan(&count))
			if count != expected {
				t.Fatalf("message count=%d expected=%d", count, expected)
			}
			must(t, db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='sqlx_scoped_sequences'").Scan(&count))
			if count != 0 {
				t.Fatal("transient allocation created a ledger")
			}
		})
	}
}

func TestMessageWriterContentBoundaryLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc, input string
		expect      int
	}
	for _, tc := range []useCase{{"ASCII content limit", strings.Repeat("x", write.MaxContentBytes+1), write.MaxContentBytes}, {"UTF8 content limit", strings.Repeat("x", write.MaxContentBytes-1) + "界", write.MaxContentBytes - 1}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := messageFixture(t, project)
			body, err := json.Marshal(map[string]any{"data": []map[string]any{{"id": "existing", "content": tc.input, "rawContent": tc.input}}})
			must(t, err)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/message", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = messageWriterRuntime(t, db).ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/message", scope)
			must(t, err)
			var content, rawContent string
			must(t, db.QueryRow("SELECT content,raw_content FROM message WHERE id='existing'").Scan(&content, &rawContent))
			if len(content) != tc.expect || len(rawContent) != tc.expect || content != rawContent {
				t.Fatalf("content lengths=%d/%d expected=%d", len(content), len(rawContent), tc.expect)
			}
		})
	}
}
