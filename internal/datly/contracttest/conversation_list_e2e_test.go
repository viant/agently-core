package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	list "github.com/viant/agently-core/internal/datly/conversation/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	"github.com/viant/bindly/locator"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	provider "github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/xdatly/state"
)

func TestConversationListVisibilityLegacyV1(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		subject string
		filters map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	for _, tc := range []useCase{
		{"anonymous excludes private and orphan rows", input{}, []string{"child", "pub"}},
		{"owner sees public and own private", input{subject: "u1"}, []string{"child", "pub", "owned"}},
		{"other owner sees their private", input{subject: "u2"}, []string{"child", "pub", "other"}},
		{"status filtering retains authorization", input{subject: "u1", filters: map[string]any{"status": "running"}}, []string{"owned"}},
		{"search retains authorization", input{subject: "u1", filters: map[string]any{"q": "public"}}, []string{"child", "pub"}},
		{"before cursor is exclusive", input{subject: "u1", filters: map[string]any{"cursorBefore": "pub"}}, []string{"owned"}},
		{"after cursor is exclusive", input{subject: "u1", filters: map[string]any{"cursorAfter": "pub"}}, []string{"child"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := conversationListFixture(t, project)
			db, _ := conversationListFixture(t, project)
			payload, err := json.Marshal(struct {
				Component, DBPath, Principal string
				Filters                      map[string]any
			}{"conversationList", oldPath, tc.input.subject, tc.input.filters})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			var stderr bytes.Buffer
			process.Stderr = &stderr
			raw, err := process.Output()
			if err != nil {
				t.Fatalf("legacy execution: %v\n%s", err, stderr.String())
			}
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			rt, key := conversationListRuntime(t, db, tc.input.subject, true)
			input := &list.ConversationInput{Has: &list.ConversationInputHas{IncludeTranscript: true, IncludeModelCal: true, IncludeToolCall: true}}
			for _, field := range []struct {
				name    string
				value   *string
				present *bool
			}{{"status", &input.StatusFilter, &input.Has.StatusFilter}, {"q", &input.Query, &input.Has.Query}, {"cursorBefore", &input.CursorBefore, &input.Has.CursorBefore}, {"cursorAfter", &input.CursorAfter, &input.Has.CursorAfter}} {
				if value, ok := tc.input.filters[field.name]; ok {
					encoded, err := json.Marshal(value)
					must(t, err)
					must(t, json.Unmarshal(encoded, field.value))
					*field.present = true
				}
			}
			output, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/conversation/{id}"}}, Input: input})
			must(t, err)
			for _, row := range output.(*list.ConversationOutput).Data {
				if row.Id == "pub" && (row.LastTurnId == nil || *row.LastTurnId != "z-pub") {
					t.Fatal("list latest-turn timestamp tie did not use descending identity")
				}
				if row.Transcript != nil || row.Usage != nil {
					t.Fatal("list selector fetched unselected transcript or usage")
				}
			}
			data, err := json.Marshal(output.(*list.ConversationOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(data, &rows))
			oldRows, newRows := normalizeConversationRows(t, before.Rows), normalizeConversationRows(t, rows)
			for i := range newRows {
				if i < len(oldRows) {
					delete(newRows[i], "transcript")
					delete(newRows[i], "usage")
				}
			}
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("list parity\nlegacy=%s\nnew=%s", pretty(oldRows), pretty(newRows))
			}
			ids := []string{}
			for _, row := range newRows {
				ids = append(ids, row["id"].(string))
			}
			if !reflect.DeepEqual(ids, tc.expect) {
				t.Fatalf("IDs=%v expected=%v", ids, tc.expect)
			}
		})
	}
}

func TestConversationListRequiresVerifiedVisibilityProvider(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	db, _ := conversationListFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file)))))
	rt, key := conversationListRuntime(t, db, "", false)
	_, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/conversation/{id}"}}, Input: &list.ConversationInput{}})
	if err == nil {
		t.Fatal("missing verified visibility source was accepted")
	}
}

func TestConversationListActivityCursorAndLatestTurnStatus(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	db, _ := conversationListFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file)))))
	_, err := db.Exec(`UPDATE conversation SET last_activity='2026-01-10 00:00:00' WHERE id='owned';
		UPDATE conversation SET last_activity='2026-01-06 00:00:00' WHERE id='pub';
		UPDATE turn SET status='failed' WHERE id='z-pub'`)
	must(t, err)
	rt, key := conversationListRuntime(t, db, "u1", true)
	readRows := func(before, after string) []*list.ConversationView {
		input := &list.ConversationInput{}
		if before != "" {
			input.SetCursorBefore(before)
		}
		if after != "" {
			input.SetCursorAfter(after)
		}
		value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{
			Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/conversation/{id}"}},
			Input:  input,
		})
		must(t, err)
		return value.(*list.ConversationOutput).Data
	}
	ids := func(rows []*list.ConversationView) []string {
		result := make([]string, 0, len(rows))
		for _, row := range rows {
			result = append(result, row.Id)
		}
		return result
	}
	all := readRows("", "")
	if got, want := ids(all), []string{"owned", "child", "pub"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("activity order=%v want %v", got, want)
	}
	if got, want := ids(readRows("child", "")), []string{"pub"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("before cursor=%v want %v", got, want)
	}
	if got, want := ids(readRows("", "child")), []string{"owned"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after cursor=%v want %v", got, want)
	}
	if all[2].Status == nil || *all[2].Status != "failed" || all[2].Stage != "error" {
		t.Fatalf("latest turn projection for pub: status=%v stage=%q", all[2].Status, all[2].Stage)
	}
}

func conversationListFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO conversation(id,title,visibility,created_by_user_id,status,created_at) VALUES
 ('pub','public root','public','other','succeeded','2026-01-03 00:00:00'),
 ('owned','private owner','private','u1','running','2026-01-02 00:00:00'),
 ('other','private other','private','u2','succeeded','2026-01-01 00:00:00'),
 ('blank-owner','must stay private','private','','succeeded','2026-01-05 00:00:00'),
 ('null-owner','must stay private','private',NULL,'succeeded','2026-01-04 00:00:00');
 INSERT INTO turn(id,conversation_id,status,created_at) VALUES('t-pub','pub','succeeded','2026-01-03 00:00:00'),
 ('z-pub','pub','succeeded','2026-01-03 00:00:00');
 INSERT INTO conversation(id,title,visibility,status,created_at,conversation_parent_id,conversation_parent_turn_id) VALUES
 ('child','public child','public','succeeded','2026-01-07 00:00:00','pub','t-pub'),
 ('orphan','orphan','public','succeeded','2026-01-08 00:00:00','missing','missing');`)
	must(t, err)
	return db, path
}

func conversationListRuntime(t *testing.T, db *sql.DB, subject string, provided bool) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(list.ReaderDatlyResourceNamespace, list.ReaderDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[list.ReaderComponent](), reflect.TypeFor[list.ConversationInput](), reflect.TypeFor[list.ConversationOutput]())
	reader, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	providers := []locator.Provider{ordinaryAccess("conversationaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "list", "enforceVisibility":
			return true, true, nil
		case "ascending":
			return false, true, nil
		}
		return nil, false, nil
	})}
	if provided {
		providers = append(providers, provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }))
	}
	var fields []string
	view := reflect.TypeFor[list.ConversationView]()
	for i := 0; i < view.NumField(); i++ {
		field := view.Field(i)
		column := strings.Split(field.Tag.Get("sqlx"), ",")[0]
		if column != "" && column != "-" {
			fields = append(fields, column)
		}
	}
	providers = append(providers, queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: fields}}}))
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[list.ConversationOutput](), Reader: reader, Providers: providers}}, druntime.WithResources(resources))
	must(t, err)
	return rt, artifact.Component.Key
}
