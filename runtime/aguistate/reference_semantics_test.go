package aguistate

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestPinnedClientReducerSemantics(t *testing.T) {
	data, err := os.ReadFile("../../protocol/agui/testdata/reducer-semantics.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name                            string
		InitialState, InitialMessages   json.RawMessage
		Events, Normalized              []json.RawMessage
		Accepted                        bool
		ExpectedMessages, ExpectedState json.RawMessage
	}
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			p, err := New(f.InitialState, f.InitialMessages)
			if err != nil {
				t.Fatal(err)
			}
			var normalized []json.RawMessage
			if f.Name == "all 31 protocol event variants" {
				if !f.Accepted {
					t.Fatal("reference rejected full-variant fixture")
				}
				kinds := map[string]bool{}
				for _, raw := range f.Events {
					var event Object
					if err := decode(raw, &event); err != nil {
						t.Fatal(err)
					}
					kinds[field(event, "type")] = true
				}
				if len(kinds) != 31 {
					t.Fatalf("fixture covers %d variants, want 31", len(kinds))
				}
			}
			for _, event := range f.Events {
				var accepted []json.RawMessage
				accepted, err = p.Apply(event)
				if err != nil {
					break
				}
				normalized = append(normalized, accepted...)
			}
			if !f.Accepted {
				if err == nil {
					t.Fatal("Go accepted sequence rejected by pinned verifier")
				}
				return
			}
			if err != nil {
				t.Fatalf("Go rejected pinned client accepted sequence: %v", err)
			}
			state, messages, err := p.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			compareJSON(t, state, f.ExpectedState, "state")
			compareJSON(t, messages, f.ExpectedMessages, "messages")
			got, _ := json.Marshal(normalized)
			want, _ := json.Marshal(f.Normalized)
			compareJSON(t, got, want, "normalized events")
		})
	}
}
func compareJSON(t *testing.T, got, want []byte, label string) {
	t.Helper()
	var a, b any
	if err := decode(got, &a); err != nil {
		t.Fatal(err)
	}
	if err := decode(want, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("%s mismatch\ngot %s\nwant %s", label, got, want)
	}
}
