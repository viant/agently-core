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
	"strings"
	"testing"

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
	legacy := legacyProbeBinary(t, project)
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
			oldDB, path := messageFixture(t, project)
			db, _ := messageFixture(t, project)
			payload, err := json.Marshal(map[string]any{"Component": "message", "DBPath": path, "Body": tc.input})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			rt := messageWriterRuntime(t, db)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/message", strings.NewReader(tc.input))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			value, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/message", scope)
			if before.Failed != tc.expect || (err != nil) != tc.expect {
				t.Fatalf("legacy=%v (%s) native=%v expected failure=%v", before.Failed, before.Error, err, tc.expect)
			}
			oldRows, newRows := messageStoredRows(t, oldDB), messageStoredRows(t, db)
			for _, rows := range [][]map[string]any{oldRows, newRows} {
				for _, row := range rows {
					for _, key := range []string{"createdat", "updatedat"} {
						if row[key] != nil {
							row[key] = "<timestamp>"
						}
					}
				}
			}
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("legacy=%s native=%s", pretty(oldRows), pretty(newRows))
			}
			if !tc.expect {
				var oldOutput []json.RawMessage
				must(t, json.Unmarshal(before.Output, &oldOutput))
				raw, err := json.Marshal(value.(*write.Output).Data)
				must(t, err)
				var newOutput []json.RawMessage
				must(t, json.Unmarshal(raw, &newOutput))
				beforeRows, afterRows := normalizeRowsInOrder(t, oldOutput), normalizeRowsInOrder(t, newOutput)
				for _, rows := range [][]map[string]any{beforeRows, afterRows} {
					for _, row := range rows {
						for _, key := range []string{"createdat", "updatedat"} {
							if row[key] != nil {
								row[key] = "<timestamp>"
							}
						}
					}
				}
				if !reflect.DeepEqual(beforeRows, afterRows) {
					t.Fatalf("response legacy=%s native=%s", before.Output, raw)
				}
			}

		})
	}
}

func messageFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO turn(id,conversation_id,status) VALUES ('t1','c1','running'),('t2','c1','running');
 INSERT INTO message(id,conversation_id,turn_id,sequence,role,type,content,status,mode,phase,iteration,interim,created_at) VALUES ('existing','c1','t1',3,'assistant','text','original','thinking','react','answer',2,1,'2026-01-01 00:00:00');`)
	must(t, err)
	return db, path
}
func messageWriterRuntime(t *testing.T, db *sql.DB, supplied ...*sql.Tx) *druntime.Runtime {
	resources := resource.New()
	must(t, resources.Register(write.WriterDatlyResourceNamespace, write.WriterDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[write.WriterComponent](), reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output]())
	views, err := viewprovider.New(viewprovider.Config{Dependencies: artifact.ViewDependencies, Input: artifact.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(artifact.Component, reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output](), "patch")
	must(t, err)
	var tx *sql.Tx
	if len(supplied) > 0 {
		tx = supplied[0]
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db, Tx: tx}}}, druntime.WithResources(resources))
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
	type useCase struct {
		desc   string
		input  bool
		expect int
	}
	for _, tc := range []useCase{{"caller rollback restores row and counter", false, 0}, {"caller commit retains row and counter", true, 1}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := messageFixture(t, project)
			// Provision the fixture ledger before observing the caller's transaction.
			_, err := db.Exec(`CREATE TABLE sqlx_scoped_sequences(scope_key TEXT PRIMARY KEY,value BIGINT NOT NULL)`)
			must(t, err)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt := messageWriterRuntime(t, db, tx)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/message", strings.NewReader(`{"data":[{"id":"new","conversationId":"c1","turnId":"t1","sequence":null,"role":"assistant","type":"text"}]}`))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/message", scope)
			must(t, err)
			var sequence int
			must(t, tx.QueryRow("SELECT sequence FROM message WHERE id='new'").Scan(&sequence))
			if sequence != 4 {
				t.Fatalf("pending sequence=%d", sequence)
			}
			if tc.input {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			for _, query := range []string{"SELECT COUNT(*) FROM message WHERE id='new'", "SELECT COUNT(*) FROM sqlx_scoped_sequences"} {
				var count int
				must(t, db.QueryRow(query).Scan(&count))
				if count != tc.expect {
					t.Fatalf("%s=%d expected=%d", query, count, tc.expect)
				}
			}
		})
	}
}

func TestMessageWriterContentBoundaryLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type useCase struct {
		desc, input string
		expect      int
	}
	for _, tc := range []useCase{{"ASCII content limit", strings.Repeat("x", write.MaxContentBytes+1), write.MaxContentBytes}, {"UTF8 content limit", strings.Repeat("x", write.MaxContentBytes-1) + "界", write.MaxContentBytes - 1}} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, path := messageFixture(t, project)
			db, _ := messageFixture(t, project)
			body, err := json.Marshal(map[string]any{"data": []map[string]any{{"id": "existing", "content": tc.input, "rawContent": tc.input}}})
			must(t, err)
			payload, err := json.Marshal(map[string]any{"Component": "message", "DBPath": path, "Body": string(body)})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed {
				t.Fatalf("legacy failed: %s", before.Error)
			}
			request := httptest.NewRequest("PATCH", "/v1/api/agently/message", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = messageWriterRuntime(t, db).ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/message", scope)
			must(t, err)
			var oldContent, oldRaw, content, rawContent string
			must(t, oldDB.QueryRow("SELECT content,raw_content FROM message WHERE id='existing'").Scan(&oldContent, &oldRaw))
			must(t, db.QueryRow("SELECT content,raw_content FROM message WHERE id='existing'").Scan(&content, &rawContent))
			if content != oldContent || rawContent != oldRaw || len(content) != tc.expect || len(rawContent) != tc.expect {
				t.Fatalf("content lengths legacy=%d/%d native=%d/%d expected=%d", len(oldContent), len(oldRaw), len(content), len(rawContent), tc.expect)
			}
		})
	}
}
