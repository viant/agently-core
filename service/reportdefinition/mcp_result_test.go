package reportdefinition

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func mcpTestColumns() []Column {
	return []Column{
		{Name: "region", Type: "string", Role: "dimension"},
		{Name: "units", Type: "integer", Role: "measure"},
		{Name: "note", Type: "string", Role: "dimension", Nullable: true},
	}
}

func TestDecodeMCPResultRecords(t *testing.T) {
	contract := ResultContract{Shape: "records", RowPath: "payload.rows", HasMorePath: "payload.page.hasMore"}
	body := []byte(`{"payload":{"rows":[{"region":"North","units":12},{"region":"South","units":0,"note":null}],"page":{"hasMore":true}}}`)
	got, err := DecodeMCPResult(contract, mcpTestColumns(), body)
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"region": "North", "units": json.Number("12")},
		{"region": "South", "units": json.Number("0"), "note": nil},
	}
	if !reflect.DeepEqual(got.Rows, want) || !got.HasMore || !got.HasMoreKnown {
		t.Fatalf("unexpected records result: %+v", got)
	}

	unknown, err := DecodeMCPResult(ResultContract{Shape: "records", RowPath: "$"}, mcpTestColumns(), []byte(`[]`))
	if err != nil || unknown.HasMore || unknown.HasMoreKnown || len(unknown.Rows) != 0 {
		t.Fatalf("bare records should have unknown completeness: %+v, %v", unknown, err)
	}
}

func TestDecodeMCPResultTabular(t *testing.T) {
	contract := ResultContract{Shape: "tabular", ResultsPath: "data", ResultName: "operations"}
	body := []byte(`{"data":[{"name":"other","columns":[],"rows":[],"hasMore":false},{"name":"operations","columns":[{"name":"units","type":"integer","role":"measure","nullable":false},{"name":"note","type":"string","role":"dimension","nullable":true},{"name":"region","type":"string","role":"dimension","nullable":false}],"rows":[[7,null,"West"]],"hasMore":false}]}`)
	got, err := DecodeMCPResult(contract, mcpTestColumns(), body)
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{{"region": "West", "units": json.Number("7"), "note": nil}}
	if !reflect.DeepEqual(got.Rows, want) || got.HasMore || !got.HasMoreKnown {
		t.Fatalf("unexpected tabular result: %+v", got)
	}
	contract.HasMorePath = "page.hasMore"
	withPath := []byte(`{"page":{"hasMore":true},"data":[{"name":"operations","columns":[{"name":"region","type":"string","role":"dimension","nullable":false},{"name":"units","type":"integer","role":"measure","nullable":false},{"name":"note","type":"string","role":"dimension","nullable":true}],"rows":[],"hasMore":true}]}`)
	got, err = DecodeMCPResult(contract, mcpTestColumns(), withPath)
	if err != nil || !got.HasMore || !got.HasMoreKnown {
		t.Fatalf("explicit tabular hasMore: result=%+v error=%v", got, err)
	}
	contract.HasMorePath = ""
	projected := []byte(`{"data":[{"name":"operations","columns":[{"name":"region","type":"string","role":"dimension","nullable":false}],"rows":[["West"]],"hasMore":false}]}`)
	got, err = DecodeMCPResult(contract, mcpTestColumns(), projected)
	if err != nil || len(got.Rows) != 1 || len(got.Rows[0]) != 1 || got.Rows[0]["region"] != "West" {
		t.Fatalf("projected tabular result: %+v, %v", got, err)
	}
	unknown := []byte(`{"data":[{"name":"operations","columns":[{"name":"region","type":"string","role":"dimension","nullable":false}],"rows":[["West"]]}]}`)
	got, err = DecodeMCPResult(contract, mcpTestColumns(), unknown)
	if err != nil || got.HasMoreKnown {
		t.Fatalf("absent tabular completeness must remain unknown: %+v, %v", got, err)
	}
}

func TestDecodeMCPResultAllowsDeclaredProjectionSubset(t *testing.T) {
	got, err := DecodeMCPResult(ResultContract{Shape: "records", RowPath: "rows", HasMorePath: "hasMore"}, mcpTestColumns(), []byte(`{"rows":[{"region":"North"}],"hasMore":false}`))
	if err != nil || len(got.Rows) != 1 || len(got.Rows[0]) != 1 || got.Rows[0]["region"] != "North" {
		t.Fatalf("projected record result: %+v, %v", got, err)
	}
}

func TestDecodeMCPResultRejectsMalformed(t *testing.T) {
	records := ResultContract{Shape: "records", RowPath: "rows", HasMorePath: "hasMore"}
	tabular := ResultContract{Shape: "tabular", ResultsPath: "data", ResultName: "operations"}
	columnJSON := `[{"name":"region","type":"string","role":"dimension","nullable":false},{"name":"units","type":"integer","role":"measure","nullable":false},{"name":"note","type":"string","role":"dimension","nullable":true}]`
	for _, test := range []struct {
		name     string
		contract ResultContract
		body     string
		want     string
	}{
		{"duplicate JSON key", records, `{"rows":[],"rows":[],"hasMore":false}`, "duplicate JSON key"},
		{"nonobject record", records, `{"rows":[["North",2]],"hasMore":false}`, "must be an object"},
		{"unknown record field", records, `{"rows":[{"region":"North","units":2,"extra":3}],"hasMore":false}`, "undeclared field"},
		{"wrong type", records, `{"rows":[{"region":"North","units":"2"}],"hasMore":false}`, "invalid value"},
		{"nonintegral value", records, `{"rows":[{"region":"North","units":2.5}],"hasMore":false}`, "invalid value"},
		{"missing rows", records, `{"hasMore":false}`, "rowPath"},
		{"missing boolean", records, `{"rows":[]}`, "hasMorePath"},
		{"ambiguous boolean", records, `{"rows":[],"hasMore":"false"}`, "boolean"},
		{"missing name", tabular, `{"data":[{"name":"other","columns":[],"rows":[],"hasMore":false}]}`, "named result"},
		{"duplicate name", tabular, `{"data":[{"name":"operations"},{"name":"operations"}]}`, "duplicate name"},
		{"wrong width", tabular, `{"data":[{"name":"operations","columns":` + columnJSON + `,"rows":[["North",2]],"hasMore":false}]}`, "wrong tabular width"},
		{"wrong column metadata", tabular, `{"data":[{"name":"operations","columns":[{"name":"region","type":"integer","role":"dimension","nullable":false},{"name":"units","type":"integer","role":"measure","nullable":false},{"name":"note","type":"string","role":"dimension","nullable":true}],"rows":[],"hasMore":false}]}`, "differs from declaration"},
		{"nonboolean tabular signal", tabular, `{"data":[{"name":"operations","columns":` + columnJSON + `,"rows":[],"hasMore":"false"}]}`, "boolean hasMore"},
		{"conflicting tabular boolean", ResultContract{Shape: "tabular", ResultsPath: "data", ResultName: "operations", HasMorePath: "page.hasMore"}, `{"page":{"hasMore":true},"data":[{"name":"operations","columns":` + columnJSON + `,"rows":[],"hasMore":false}]}`, "conflicting hasMore"},
		{"error response", records, `{"status":"error","rows":[],"hasMore":false}`, "status is not ok"},
		{"trailing JSON", records, `{"rows":[],"hasMore":false}{}`, "exactly one JSON"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := DecodeMCPResult(test.contract, mcpTestColumns(), []byte(test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) || len(got.Rows) != 0 || got.HasMoreKnown {
				t.Fatalf("result=%+v, error=%v; want %q", got, err, test.want)
			}
		})
	}
}

func TestDecodeMCPResultPaginationAndBounds(t *testing.T) {
	records := ResultContract{Shape: "records", RowPath: "rows", HasMorePath: "hasMore"}
	for _, more := range []bool{true, false} {
		body := `{"rows":[],"hasMore":false}`
		if more {
			body = `{"rows":[],"hasMore":true}`
		}
		got, err := DecodeMCPResult(records, mcpTestColumns(), []byte(body))
		if err != nil || got.HasMore != more || !got.HasMoreKnown {
			t.Fatalf("hasMore=%v: result=%+v error=%v", more, got, err)
		}
	}
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"body bytes", strings.Repeat(" ", maxDatasetBytes+1), "structuredContent"},
		{"page rows", `{"rows":[` + strings.Repeat(`{},`, maxPageSize) + `{ }],"hasMore":false}`, "exceeds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeMCPResult(records, mcpTestColumns(), []byte(test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v; want %q", err, test.want)
			}
		})
	}
}
