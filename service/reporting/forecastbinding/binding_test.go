package forecastbinding

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

type fixture struct {
	Scope     Scope           `json:"scope"`
	Policy    Policy          `json:"policy"`
	Bindings  Bindings        `json:"bindings"`
	Records   []Call          `json:"records"`
	Incorrect json.RawMessage `json:"incorrectAuthoredData"`
	Expected  json.RawMessage `json:"expectedData"`
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	b, e := os.ReadFile("testdata/actual_forecast_20_calls.json")
	if e != nil {
		t.Fatal(e)
	}
	var f fixture
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	return f
}
func TestActualTwentyCallsMaterializeFifteenCellsWithoutReorderingAssociations(t *testing.T) {
	f := loadFixture(t)
	before, _ := json.Marshal(f)
	got, e := Materialize(f.Scope, f.Policy, f.Bindings, f.Records)
	if e != nil {
		t.Fatal(e)
	}
	want, _ := canonical(f.Expected)
	if !bytes.Equal(got.Data, want) {
		t.Fatalf("wrong rows: %s", got.Data)
	}
	if len(f.Records) != 20 || len(got.Provenance) != 15 {
		t.Fatal("fixture/provenance coverage")
	}
	if _, e = Validate(f.Scope, f.Policy, f.Bindings, f.Records, f.Incorrect); e == nil {
		t.Fatal("swapped Oct2/3 must fail")
	}
	if _, e = Validate(f.Scope, f.Policy, f.Bindings, f.Records, f.Expected); e != nil {
		t.Fatal(e)
	}
	after, _ := json.Marshal(f)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated immutable evidence/body")
	}
	for i, j := 0, len(f.Records)-1; i < j; i, j = i+1, j-1 {
		f.Records[i], f.Records[j] = f.Records[j], f.Records[i]
	}
	for i := range f.Bindings.Columns {
		refs := f.Bindings.Columns[i].Calls
		refs[0], refs[2] = refs[2], refs[0]
	}
	reordered, e := Materialize(f.Scope, f.Policy, f.Bindings, f.Records)
	if e != nil || !bytes.Equal(reordered.Data, got.Data) {
		t.Fatalf("arrival order affected output: %v", e)
	}
}
func firstCall(f *fixture) *Call {
	op := f.Bindings.Columns[0].Calls[0].OpID
	for i := range f.Records {
		if f.Records[i].OpID == op {
			return &f.Records[i]
		}
	}
	panic("fixture missing call")
}
func changeRequest(f *fixture, edit func(map[string]any)) {
	c := firstCall(f)
	r, _ := object(c.Request)
	edit(r)
	c.Request, _ = json.Marshal(r)
	f.Bindings.Columns[0].Calls[0].RequestHash, _ = RequestHash(c.Request)
}
func changeResponse(f *fixture, edit func(map[string]any)) {
	c := firstCall(f)
	r, _ := object(c.Response)
	edit(r)
	c.Response, _ = json.Marshal(r)
}
func TestEvidenceFailuresRejectCompletion(t *testing.T) {
	cases := map[string]func(*fixture){
		"owner":        func(f *fixture) { firstCall(f).OwnerID = "foreign" },
		"conversation": func(f *fixture) { firstCall(f).ConversationID = "foreign" },
		"turn":         func(f *fixture) { firstCall(f).TurnID = "foreign" },
		"hash":         func(f *fixture) { f.Bindings.Columns[0].Calls[0].RequestHash = "bad" },
		"pointer":      func(f *fixture) { f.Bindings.Columns[0].Calls[0].ResponsePointer = "/data/0/deviceUniqs" },
		"missingOp":    func(f *fixture) { f.Bindings.Columns[0].Calls[0].OpID = "missing" },
		"failed":       func(f *fixture) { firstCall(f).Status = "failed" },
		"wrongTool":    func(f *fixture) { firstCall(f).Tool = "other" },
		"scopeEvenWithNewHash": func(f *fixture) {
			changeRequest(f, func(r map[string]any) { r["filters"].(map[string]any)["includeMetrocode"] = []any{json.Number("999")} })
		},
		"wrongDay": func(f *fixture) {
			changeRequest(f, func(r map[string]any) { r["filters"].(map[string]any)["date"] = "2026-10-01T00:00:00Z" })
		},
		"duplicateDate": func(f *fixture) { f.Bindings.Columns[0].Calls[1] = f.Bindings.Columns[0].Calls[0] },
		"missingDate":   func(f *fixture) { f.Bindings.Columns[0].Calls = f.Bindings.Columns[0].Calls[:2] },
		"missingColumn": func(f *fixture) { f.Bindings.Columns = f.Bindings.Columns[:4] },
		"duplicateConflictingRecord": func(f *fixture) {
			c := *firstCall(f)
			c.Response = json.RawMessage(`{"status":"ok","data":[{"avails":1}]}`)
			f.Records = append(f.Records, c)
		},
		"conflictingResponseDate": func(f *fixture) {
			changeResponse(f, func(r map[string]any) { r["data"].([]any)[0].(map[string]any)["eventDate"] = "2026-10-03T00:00:00Z" })
		},
		"unsuccessfulResponse": func(f *fixture) { changeResponse(f, func(r map[string]any) { r["status"] = "error" }) },
		"missingRow":           func(f *fixture) { changeResponse(f, func(r map[string]any) { r["data"] = []any{} }) },
		"nonnumeric": func(f *fixture) {
			changeResponse(f, func(r map[string]any) { r["data"].([]any)[0].(map[string]any)["avails"] = "397765284" })
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := loadFixture(t)
			change(&f)
			if _, e := Materialize(f.Scope, f.Policy, f.Bindings, f.Records); e == nil {
				t.Fatal("accepted invalid evidence")
			}
		})
	}
}
