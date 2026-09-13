package tablepreference

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestContractRejectsUnsafeOrAmbiguousPreferences(t *testing.T) {
	for _, body := range []string{
		`{"version":2}`, `{"version":1,"rows":[{"secret":"data"}]}`,
		`{"version":1,"columns":[{"id":"a","handler":"execute"}]}`,
		`{"version":1,"columns":[{"id":"a","width":0}]}`,
		`{"version":1,"columns":[{"id":"a","width":4097}]}`,
		`{"version":1,"columns":[{"id":"a"},{"id":"a"}]}`,
		`{"version":1,"sort":{"columnId":"a","direction":"random"}}`,
		`{"version":1,"frozenColumnIds":["a","a"]}`, `{"version":1} {}`,
		`null`,
	} {
		if _, err := Decode([]byte(body)); err == nil {
			t.Errorf("accepted invalid document: %s", body)
		}
	}
	if _, err := Decode([]byte(strings.Repeat(" ", MaxBytes) + `{"version":1}`)); err == nil {
		t.Fatal("unbounded payload accepted")
	}
}

func TestContractPreservesOrderAndFalseVisibility(t *testing.T) {
	p, err := Decode([]byte(`{"version":1,"columns":[{"id":"b","visible":false,"width":240},{"id":"a"}],"sort":{"columnId":"b","direction":"desc"},"density":"normal","frozenColumnIds":["a"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Columns[0].ID != "b" || p.Columns[0].Visible == nil || *p.Columns[0].Visible {
		t.Fatal("preference semantics changed")
	}
	if !json.Valid(Schema) {
		t.Fatal("invalid published schema")
	}
}

type fakeTransport struct {
	calls     int
	operation string
	request   Request
	body      json.RawMessage
	err       error
}

func (f *fakeTransport) Call(_ context.Context, operation string, body json.RawMessage) (json.RawMessage, error) {
	f.calls++
	f.operation = operation
	if err := json.Unmarshal(body, &f.request); err != nil {
		return nil, err
	}
	return f.body, f.err
}

func TestClientValidatesBothSidesOfExternalBoundary(t *testing.T) {
	f := &fakeTransport{body: json.RawMessage(`{"version":1,"columns":[{"id":"name","visible":false}]}`)}
	c, err := NewClient(f)
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Get(context.Background(), "workspace/table")
	if err != nil || p.Columns[0].ID != "name" {
		t.Fatalf("get: %v %v", p, err)
	}
	if f.operation != "get" || f.request.Key != "workspace/table" {
		t.Fatal("wrong normalized request")
	}
	f.body = json.RawMessage(`{"version":1,"rows":[]}`)
	if _, err = c.Get(context.Background(), "table"); err == nil {
		t.Fatal("external runtime data accepted")
	}
	f.body = json.RawMessage(`{}`)
	if _, err = c.Get(context.Background(), "table"); err == nil {
		t.Fatal("missing response treated as missing preference")
	}
	f.body = json.RawMessage(`null`)
	if p, err = c.Get(context.Background(), "table"); err != nil || p != nil {
		t.Fatal("explicit absence rejected")
	}
	before := f.calls
	if err = c.Set(context.Background(), "table", &Preferences{Version: 2}); err == nil || f.calls != before {
		t.Fatal("invalid write crossed boundary")
	}
	if err = c.Reset(context.Background(), ""); err == nil || f.calls != before {
		t.Fatal("invalid key crossed boundary")
	}
	f.body = json.RawMessage(`{}`)
	if err = c.Set(context.Background(), "table", &Preferences{Version: 1}); err != nil || f.operation != "set" {
		t.Fatal("valid write failed", err)
	}
	if err = c.Reset(context.Background(), "table"); err != nil || f.operation != "reset" {
		t.Fatal("reset failed", err)
	}
	f.err = errors.New("external adapter unavailable")
	if _, err = c.Get(context.Background(), "table"); !errors.Is(err, f.err) {
		t.Fatal("adapter failure hidden")
	}
}
