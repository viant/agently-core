package tests

import (
	"context"
	requestprovider "github.com/viant/bindly/provider/request"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	write "github.com/viant/agently-core/internal/datly/message/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

func TestMessageWriterTrustedTerminalCleanup(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	captured := time.Date(2026, 1, 7, 3, 4, 5, 123000000, time.UTC)
	cases := []struct {
		name      string
		configure func(*write.Message)
		failure   string
		both      bool
		zero      bool
	}{
		{name: "captured timestamp", configure: func(*write.Message) {}},
		{name: "explicit zero timestamp", configure: func(row *write.Message) { zero := time.Time{}; row.SetUpdatedAt(&zero) }, zero: true},
		{name: "reject missing timestamp", configure: func(row *write.Message) { row.UpdatedAt = nil; row.Has.UpdatedAt = false }, failure: "explicit timestamp"},
		{name: "reject non-failed status", configure: func(row *write.Message) { status := "running"; row.SetStatus(&status) }, failure: "failed status"},
		{name: "reject extra content", configure: func(row *write.Message) { text := "changed"; row.SetContent(&text) }, failure: "cannot change other fields"},
		{name: "reject delete marker", configure: func(row *write.Message) { row.SetShouldDelete(true) }, failure: "cannot change other fields"},
		{name: "reject multiple modes", configure: func(*write.Message) {}, failure: "mutually exclusive", both: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := messageFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(source)))))
			before := messageStoredRows(t, db)
			row := &write.Message{}
			row.SetId("existing")
			status := "failed"
			row.SetStatus(&status)
			row.SetUpdatedAt(&captured)
			tc.configure(row)
			input := &write.Input{}
			input.SetMessages([]*write.Message{row})
			trusted := provider.Named("messageaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
				switch name {
				case "terminalCleanup":
					return true, true, nil
				case "detachLinks":
					return tc.both, true, nil
				}
				return nil, false, nil
			})
			rt := messageWriterRuntime(t, db)
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/message"}}, Input: input, Providers: []locator.Provider{trusted}})
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) {
					t.Fatalf("expected %q, got %v", tc.failure, err)
				}
				if after := messageStoredRows(t, db); !reflect.DeepEqual(before, after) {
					t.Fatalf("failed cleanup changed rows: %v => %v", before, after)
				}
				return
			}
			must(t, err)
			output := value.(*write.Output)
			if len(output.Data) != 1 {
				t.Fatalf("missing output: %#v", output)
			}
			has := output.Data[0].Has
			if !has.Status || !has.UpdatedAt || has.Role || has.Type || has.ConversationId || has.TurnId || has.Sequence || has.CreatedAt || has.Content {
				t.Fatalf("cleanup changed omitted presence: %#v", has)
			}
			after := messageStoredRows(t, db)
			for _, rows := range [][]map[string]any{before, after} {
				for _, item := range rows {
					if item["id"] == "existing" {
						delete(item, "status")
						delete(item, "updatedat")
					}
				}
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("cleanup changed omitted columns: %v => %v", before, after)
			}
			var gotStatus string
			var gotTime time.Time
			must(t, db.QueryRow(`SELECT status,updated_at FROM message WHERE id='existing'`).Scan(&gotStatus, &gotTime))
			expected := captured
			if tc.zero {
				expected = time.Time{}
			}
			if gotStatus != "failed" || !gotTime.Equal(expected) {
				t.Fatalf("captured update lost: %s %s expected %s", gotStatus, gotTime, expected)
			}
		})
	}
}

func TestMessageWriterCleanupModeCannotComeFromHTTPRequest(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	db, _ := messageFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(source)))))
	rt := messageWriterRuntime(t, db)
	request := httptest.NewRequest("PATCH", "/v1/api/agently/message?terminalCleanup=true&detachLinks=true", strings.NewReader(`{"terminalCleanup":true,"detachLinks":true,"data":[{"id":"existing","status":"failed","updatedAt":"2000-01-01T00:00:00Z"}]}`))
	request.Header.Set("Content-Type", "application/json")
	scope, err := requestprovider.New(request)
	must(t, err)
	defer scope.Close()
	value, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/message", scope)
	must(t, err)
	row := value.(*write.Output).Data[0]
	if row.UpdatedAt == nil || row.UpdatedAt.Year() == 2000 || !row.Has.Role || !row.Has.ConversationId {
		t.Fatalf("HTTP input activated private cleanup mode: %#v %#v", row, row.Has)
	}
}
