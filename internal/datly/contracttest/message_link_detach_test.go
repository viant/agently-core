package tests

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	write "github.com/viant/agently-core/internal/datly/message/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

func TestMessageWriterTrustedGraphDetach(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(source))))
	cases := []struct {
		name      string
		configure func(*write.Message)
		targets   []string
		failure   string
	}{
		{"clear both links preserving timestamps", func(row *write.Message) { row.SetParentMessageId(nil); row.SetSupersededBy(nil) }, []string{"anchor"}, ""},
		{"reject delete marker", func(row *write.Message) { row.SetParentMessageId(nil); row.SetShouldDelete(true) }, []string{"anchor"}, "cannot change other fields"},
		{"reject unrelated content", func(row *write.Message) { row.SetParentMessageId(nil); text := "changed"; row.SetContent(&text) }, []string{"anchor"}, "cannot change other fields"},
		{"reject non-null replacement", func(row *write.Message) { value := "anchor"; row.SetParentMessageId(&value) }, []string{"anchor"}, "outside the trusted graph"},
		{"reject targets outside graph", func(row *write.Message) { row.SetParentMessageId(nil) }, []string{"outside"}, "outside the trusted graph"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := messageFixture(t, project)
			_, err := db.Exec(`INSERT INTO message(id,conversation_id,role,type,created_at) VALUES ('anchor','c1','user','text','2026-01-01 00:00:00'); UPDATE message SET parent_message_id='anchor',superseded_by='anchor',updated_at='2026-01-02 03:04:05' WHERE id='existing'`)
			must(t, err)
			before := messageStoredRows(t, db)
			row := &write.Message{}
			row.SetId("existing")
			tc.configure(row)
			input := &write.Input{}
			input.SetMessages([]*write.Message{row})
			trusted := provider.Named("messageaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
				switch name {
				case "detachLinks":
					return true, true, nil
				case "messageIds":
					return tc.targets, true, nil
				}
				return nil, false, nil
			})
			rt := messageWriterRuntime(t, db)
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/message"}}, Input: input, Providers: []locator.Provider{trusted}})
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) {
					t.Fatalf("expected %q, got %v", tc.failure, err)
				}
				after := messageStoredRows(t, db)
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("failed detach changed rows: before=%v after=%v", before, after)
				}
				return
			}
			must(t, err)
			output := value.(*write.Output)
			if len(output.Data) != 1 || output.Data[0].Has.UpdatedAt {
				t.Fatalf("detach added timestamp presence: %#v", output)
			}
			after := messageStoredRows(t, db)
			for _, rows := range [][]map[string]any{before, after} {
				for _, item := range rows {
					if item["id"] == "existing" {
						delete(item, "parentmessageid")
						delete(item, "supersededby")
					}
				}
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("detach changed unrelated data: before=%v after=%v", before, after)
			}
			var parent, superseded any
			must(t, db.QueryRow(`SELECT parent_message_id,superseded_by FROM message WHERE id='existing'`).Scan(&parent, &superseded))
			if parent != nil || superseded != nil {
				t.Fatalf("links retained: %v %v", parent, superseded)
			}
		})
	}
}
