package schedule

import (
	"encoding/json"
	read "github.com/viant/agently-core/internal/datly/schedule/read"
	"testing"
)

var _ *read.ScheduleView = (*ScheduleView)(nil)

func TestScheduleGeneratedAliasRetainsScalarShapeAndHidesRawLease(t *testing.T) {
	rawLease := "corrupt"
	v := &ScheduleView{Id: "owned", Name: "ORANGE-42 café", Enabled: false, TimeoutSeconds: 0, LeaseUntilRaw: &rawLease}
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 29 || string(wire["enabled"]) != "false" || string(wire["timeoutSeconds"]) != "0" || string(wire["leaseUntil"]) != "null" {
		t.Fatal("public scalar/null/zero shape changed")
	}
	if _, ok := wire["leaseUntilRaw"]; ok {
		t.Fatal("internal raw lease leaked")
	}
	var out ScheduleView
	if err = json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != v.Name {
		t.Fatal("UTF8 name changed")
	}
}
