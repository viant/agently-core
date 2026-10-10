package datasource

import (
	"github.com/viant/forge/backend/types"
	"reflect"
	"testing"
)

func TestLegacyPagingEmptyModeRetainsExistingSlicingAndLiteralOffset(t *testing.T) {
	rows := []map[string]interface{}{{"id": 0}, {"id": 1}, {"id": 2}, {"id": 3}, {"id": 4}}
	ds := &types.DataSource{Paging: &types.PagingConfig{Enabled: true, Size: 2, Parameters: &types.PagingParameters{Page: "pageNumber", Size: "batch"}}}
	got, info := applyPaging(rows, nil, ds, map[string]interface{}{"pageNumber": 3, "batch": 2})
	if len(got) != 1 || got[0]["id"] != 4 || info["page"] != 3 || info["totalCount"] != 5 {
		t.Fatalf("legacy page behavior changed: %#v %#v", got, info)
	}
	ds.Paging.Parameters.Page = "Offset"
	got, info = applyPaging(rows[:2], nil, ds, map[string]interface{}{"Offset": 4, "batch": 2})
	if !reflect.DeepEqual(got, rows[:2]) || info["page"] != 3 || info["hasMore"] != true {
		t.Fatalf("legacy literal-offset behavior changed: %#v %#v", got, info)
	}
}
func TestExplicitPagingDisabledNeverChangesRowsOrInfo(t *testing.T) {
	rows := []map[string]interface{}{{"id": 1}}
	info := map[string]interface{}{"custom": "kept"}
	ds := &types.DataSource{Paging: &types.PagingConfig{Enabled: false, Mode: "unsupported"}}
	if validateExplicitPaging(ds) != nil {
		t.Fatal("disabled paging must not impose capability requirements")
	}
	got, actual := applyPaging(rows, info, ds, nil)
	if !reflect.DeepEqual(got, rows) || !reflect.DeepEqual(actual, info) {
		t.Fatal("disabled paging altered backend result")
	}
}
func TestExplicitOffsetHonorsKnownZeroAndDoesNotMutateUpstreamInfo(t *testing.T) {
	rows := []map[string]interface{}{}
	info := map[string]interface{}{"count": 0, "opaque": "kept"}
	ds := &types.DataSource{Paging: &types.PagingConfig{Enabled: true, Mode: "offset", Size: 2, Parameters: &types.PagingParameters{Page: "skipRows", Size: "takeRows"}, DataInfoSelectors: &types.DataInfoSelectors{TotalCount: "count"}}}
	_, got := applyPaging(rows, info, ds, map[string]interface{}{"skipRows": 0, "takeRows": 2})
	if got["totalCount"] != 0 || got["hasMore"] != false || got["opaque"] != "kept" {
		t.Fatalf("zero total/opaque info lost: %#v", got)
	}
	if len(info) != 2 {
		t.Fatal("upstream info was mutated")
	}
}
func TestScalarValueColumnIsExplicitAndPreservesZeroFalseAndArrays(t *testing.T) {
	for _, value := range []interface{}{false, float64(0), "", []interface{}{false, float64(0), ""}} {
		legacy, _, _ := project(value, nil)
		if len(legacy) != 0 {
			t.Fatal("legacy scalar projection unexpectedly changed")
		}
		ds := &types.DataSource{Selectors: &types.Selectors{ValueColumn: "answer"}}
		got, _, _ := project(value, ds)
		if list, ok := value.([]interface{}); ok {
			if len(got) != len(list) {
				t.Fatal("scalar array dropped")
			}
			for i, v := range list {
				if !reflect.DeepEqual(got[i]["answer"], v) {
					t.Fatal("scalar array value altered")
				}
			}
		} else if len(got) != 1 || !reflect.DeepEqual(got[0]["answer"], value) {
			t.Fatalf("scalar lost: %#v", got)
		}
	}
}
