package feedextract

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/forge/backend/types"
)

func TestExtract_BasicAndCountBehavior(t *testing.T) {
	spec := &Spec{
		ID:          "tasks",
		CountSource: "items",
		DataSources: map[string]*DataSource{
			"query": {
				Name:     "query",
				Source:   "input",
				ExposeAs: "input",
			},
			"snapshot": {
				Name:     "snapshot",
				Source:   "output",
				Merge:    "replace_last",
				ExposeAs: "output",
			},
			"items": {
				Name: "items",
				DataSource: types.DataSource{
					DataSourceRef: "snapshot",
					Selectors:     &types.Selectors{Data: "items"},
				},
				Root: true,
				Derive: map[string]string{
					"label": "${id}:${status}",
				},
			},
		},
	}
	got, err := Extract(&Input{
		Spec:            spec,
		RequestPayloads: []string{`{"q":"abc"}`},
		ResponsePayloads: []string{
			`{"items":[{"id":"a","status":"done"},{"id":"b","status":"pending"}]}`,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "items", got.RootName)
	assert.Equal(t, 2, got.ItemCount)
	assert.JSONEq(t, `{"input":{"q":"abc"},"output":{"items":[{"id":"a","status":"done","label":"a:done"},{"id":"b","status":"pending","label":"b:pending"}]}}`, mustJSON(t, got.RootData))
}

func TestExtract_NilInputAndSpec(t *testing.T) {
	got, err := Extract(nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = Extract(&Input{})
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = Extract(&Input{Spec: &Spec{DataSources: map[string]*DataSource{"   ": {}}}})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "", got.RootName)
	assert.Equal(t, 0, got.ItemCount)
}

func TestExtract_CycleError(t *testing.T) {
	_, err := Extract(&Input{
		Spec: &Spec{
			DataSources: map[string]*DataSource{
				"a": {DataSource: types.DataSource{DataSourceRef: "b"}},
				"b": {DataSource: types.DataSource{DataSourceRef: "a"}},
			},
		},
	})
	require.Error(t, err)
}

func TestResolveSourceAndMergeValues(t *testing.T) {
	requests := parsePayloadList([]string{`{"q":"abc"}`})
	responses := parsePayloadList([]string{
		`{"items":[{"id":"a"}],"meta":{"x":1}}`,
		`{"items":[{"id":"b"}],"meta":{"y":2}}`,
	})
	assert.JSONEq(t, `[{"id":"a"},{"id":"b"}]`, mustJSON(t, resolveSource("output.items", requests, responses, "append")))
	assert.JSONEq(t, `{"x":1,"y":2}`, mustJSON(t, resolveSource("output.meta", requests, responses, "merge_object")))
	assert.JSONEq(t, `{"q":"abc"}`, mustJSON(t, resolveSource("input", requests, responses, "")))
	assert.JSONEq(t, `{"items":[{"id":"b"}],"meta":{"y":2}}`, mustJSON(t, resolveSource("output", requests, responses, "")))
	assert.Nil(t, resolveSource("output.none", requests, responses, "replace_last"))
}

func TestDefaultMergeAndMergeInputs(t *testing.T) {
	assert.Equal(t, "replace_last", defaultMerge("input", nil))
	assert.Equal(t, "replace_last", defaultMerge("output", nil))
	assert.Equal(t, "append", defaultMerge("output.items", []interface{}{[]interface{}{1}}))
	assert.Equal(t, "replace_last", defaultMerge("output.items", []interface{}{map[string]interface{}{"a": 1}}))
	assert.JSONEq(t, `[1,2,3]`, mustJSON(t, mergeInputs([]interface{}{[]interface{}{1, 2}, []interface{}{3}}, "")))
}

func TestMergeJSONPreserveFirst(t *testing.T) {
	assert.JSONEq(t, `{"a":1,"b":2}`, mustJSON(t, mergeJSONPreserveFirst(map[string]interface{}{"a": 1}, map[string]interface{}{"b": 2})))
	assert.JSONEq(t, `[1,2,3]`, mustJSON(t, mergeJSONPreserveFirst([]interface{}{1, 2}, []interface{}{3})))
	assert.JSONEq(t, `[1,2]`, mustJSON(t, mergeJSONPreserveFirst([]interface{}{1}, 2)))
	assert.JSONEq(t, `[1,2]`, mustJSON(t, mergeJSONPreserveFirst(1, []interface{}{2})))
	assert.Equal(t, "a", mergeJSONPreserveFirst("a", "b"))
	assert.JSONEq(t, `[1,2,3]`, mustJSON(t, mergeJSONPreserveFirst([]interface{}{1, 2}, 3)))
	assert.JSONEq(t, `[1,2,3]`, mustJSON(t, mergeJSONPreserveFirst(1, []interface{}{2, 3})))
}

func TestDedupeByUniqueKey(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{"id": "a", "status": "done"},
		map[string]interface{}{"id": "a", "status": "done"},
		map[string]interface{}{"status": "missing-key"},
		map[string]interface{}{"status": "missing-key"},
	}
	got := dedupeByUniqueKey(items, []*types.UniqueKey{{Field: "id"}}).([]interface{})
	require.Len(t, got, 3)
}

func TestApplyDeriveAndTemplate(t *testing.T) {
	mapValue := map[string]interface{}{"id": "a", "status": "done"}
	assert.JSONEq(t, `{"id":"a","status":"done","label":"a:done","missing":""}`, mustJSON(t, applyDerive(mapValue, map[string]string{
		"label":   "${id}:${status}",
		"missing": "${none}",
	})))

	sliceValue := []interface{}{
		map[string]interface{}{"id": "a"},
		"x",
	}
	assert.JSONEq(t, `[{"id":"a","label":"a"}, "x"]`, mustJSON(t, applyDerive(sliceValue, map[string]string{"label": "${id}"})))
	assert.Equal(t, "x", applyDerive("x", map[string]string{"label": "${id}"}))
	assert.Equal(t, "", renderTemplate("${missing}", map[string]interface{}{"id": "a"}))
}

func TestRootAndItemCountHelpers(t *testing.T) {
	resolved := map[string]interface{}{
		"a": []interface{}{},
		"b": []interface{}{1, 2},
	}
	spec := &Spec{
		DataSources: map[string]*DataSource{
			"a": {Root: false},
			"b": {Root: true},
		},
		CountSource: "a",
	}
	assert.Equal(t, "b", rootDataSourceName(spec, resolved))
	assert.Equal(t, 0, computeItemCount(spec, resolved, "b"))
	assert.Equal(t, 2, computeItemCount(&Spec{DataSources: spec.DataSources}, resolved, "b"))
	assert.Equal(t, 2, itemCount([]interface{}{1, 2}))
	assert.Equal(t, 1, itemCount(map[string]interface{}{"a": 1}))
	assert.Equal(t, 0, itemCount(map[string]interface{}{}))
	assert.Equal(t, 1, itemCount("x"))
}

func TestProjectLegacyRootAndSelectorField(t *testing.T) {
	spec := &Spec{
		DataSources: map[string]*DataSource{
			"inputDs":  {Source: "input"},
			"outputDs": {Source: "output"},
			"items": {
				DataSource: types.DataSource{
					DataSourceRef: "outputDs",
					Selectors:     &types.Selectors{Data: "items"},
				},
			},
			"custom": {ExposeAs: "entries"},
		},
	}
	resolved := map[string]interface{}{
		"inputDs":  map[string]interface{}{"q": "abc"},
		"outputDs": map[string]interface{}{"items": []interface{}{map[string]interface{}{"id": "a"}}},
		"items":    []interface{}{map[string]interface{}{"id": "a"}},
		"custom":   []interface{}{1, 2},
	}
	got := projectLegacyRoot(spec, resolved)
	assert.JSONEq(t, `{"input":{"q":"abc"},"output":{"items":[{"id":"a"}]},"entries":[1,2]}`, mustJSON(t, got))
	assert.Equal(t, "items", selectorField(&types.Selectors{Data: "items"}))
	assert.Equal(t, "", selectorField(&types.Selectors{Data: "items.id"}))
	assert.Equal(t, "", selectorField(nil))
}

func TestSelectPath(t *testing.T) {
	root := map[string]interface{}{
		"input": map[string]interface{}{"q": "abc"},
		"output": map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"id": "a"},
			},
		},
	}
	assert.Equal(t, root, selectPath("", root))
	assert.JSONEq(t, `{"q":"abc"}`, mustJSON(t, selectPath("input", root)))
	assert.JSONEq(t, `[{"id":"a"}]`, mustJSON(t, selectPath("output.items", root)))
	assert.JSONEq(t, `{"id":"a"}`, mustJSON(t, selectPath("output.items[0]", root)))
	assert.Nil(t, selectPath("output.items[1]", root))
	assert.Nil(t, selectPath("output.none", root))

	flat := map[string]interface{}{"items": []interface{}{1, 2}}
	assert.JSONEq(t, `[1,2]`, mustJSON(t, selectPath("output.items", flat)))
}

func TestExtract_AdditionalBranches(t *testing.T) {
	t.Run("nil datasource resolves empty collection", func(t *testing.T) {
		got, err := Extract(&Input{
			Spec: &Spec{
				DataSources: map[string]*DataSource{
					"missing": nil,
				},
				CountSource: "missing",
			},
		})
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, 0, got.ItemCount)
		assert.Equal(t, "", got.RootName)
	})

	t.Run("raw input references and empty datasource default", func(t *testing.T) {
		got, err := Extract(&Input{
			Spec: &Spec{
				DataSources: map[string]*DataSource{
					"refs": {
						Inputs: []string{"output.items", "missing"},
						Merge:  "union",
						Root:   true,
					},
					"empty": {},
				},
			},
			ResponsePayloads: []string{`{"items":[{"id":"a"}]}`},
		})
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "refs", got.RootName)
		assert.Equal(t, 2, got.ItemCount)
		assert.JSONEq(t, `{"input":{},"output":{} }`, mustJSON(t, projectLegacyRoot(&Spec{DataSources: map[string]*DataSource{"empty": {}}}, map[string]interface{}{"empty": []interface{}{}})))
	})

	t.Run("parent datasource array root and default selector", func(t *testing.T) {
		got, err := Extract(&Input{
			Spec: &Spec{
				DataSources: map[string]*DataSource{
					"parent": {
						Source: "output.items",
					},
					"child": {
						DataSource: types.DataSource{
							DataSourceRef: "parent",
						},
						Root: true,
					},
				},
			},
			ResponsePayloads: []string{`{"items":[{"output":{"x":1}},{"output":{"x":2}}]}`},
		})
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, 2, got.ItemCount)
	})
}

func TestSplitSourceAndMergeValuesAdditionalBranches(t *testing.T) {
	scope, path := splitSource("input")
	assert.Equal(t, "input", scope)
	assert.Equal(t, "", path)
	scope, path = splitSource("output")
	assert.Equal(t, "output", scope)
	assert.Equal(t, "", path)
	scope, path = splitSource("foo.bar")
	assert.Equal(t, "output", scope)
	assert.Equal(t, "foo.bar", path)
	scope, path = splitSource("output.items")
	assert.Equal(t, "output", scope)
	assert.Equal(t, "items", path)
	scope, path = splitSource("input.items")
	assert.Equal(t, "input", scope)
	assert.Equal(t, "items", path)

	assert.Nil(t, mergeValues(nil, "", "replace_last"))
	assert.JSONEq(t, `[1,2]`, mustJSON(t, mergeValues([]interface{}{1, 2}, "", "append")))
	assert.JSONEq(t, `{"a":1}`, mustJSON(t, mergeValues([]interface{}{map[string]interface{}{"a": 1}}, "", "merge_object")))
}

func TestMergeJSONPreserveFirstAdditionalBranches(t *testing.T) {
	assert.Equal(t, "b", mergeJSONPreserveFirst(nil, "b"))
	assert.Equal(t, "a", mergeJSONPreserveFirst("a", nil))
	assert.JSONEq(t, `{"a":1,"b":2}`, mustJSON(t, mergeJSONPreserveFirst(map[string]interface{}{"a": 1}, map[string]interface{}{"b": 2})))
}

func TestDedupeByUniqueKeyAdditionalBranches(t *testing.T) {
	assert.Equal(t, "x", dedupeByUniqueKey("x", []*types.UniqueKey{{Field: "id"}}))
	items := []interface{}{"x", map[string]interface{}{"id": "a"}, map[string]interface{}{"id": "a"}}
	got := dedupeByUniqueKey(items, []*types.UniqueKey{{Field: "id"}}).([]interface{})
	require.Len(t, got, 2)
}

func TestRenderTemplateAndRootHelpersAdditionalBranches(t *testing.T) {
	assert.Equal(t, "plain", renderTemplate("plain", map[string]interface{}{"id": "a"}))
	assert.Equal(t, "${}", renderTemplate("${}", map[string]interface{}{"id": "a"}))

	assert.Equal(t, "", rootDataSourceName(nil, nil))
	assert.Equal(t, "b", rootDataSourceName(&Spec{DataSources: map[string]*DataSource{"a": {}, "b": {}}}, map[string]interface{}{"a": []interface{}{}, "b": []interface{}{1}}))
	assert.Equal(t, "", rootDataSourceName(&Spec{DataSources: map[string]*DataSource{"a": {}}}, map[string]interface{}{"a": []interface{}{}}))

	assert.Equal(t, 0, computeItemCount(nil, nil, ""))
	assert.Equal(t, 1, computeItemCount(&Spec{CountSource: "missing"}, map[string]interface{}{"a": "x"}, "a"))
	assert.Equal(t, 0, itemCount(nil))
}

func TestProjectLegacyRootAdditionalBranches(t *testing.T) {
	spec := &Spec{
		DataSources: map[string]*DataSource{
			"inputDs":  {Source: "input"},
			"outputDs": {Source: "output"},
			"nested": {
				DataSource: types.DataSource{
					DataSourceRef: "outputDs",
					Selectors:     &types.Selectors{Data: "items.id"},
				},
			},
			"badParent": {
				DataSource: types.DataSource{
					DataSourceRef: "scalar",
					Selectors:     &types.Selectors{Data: "items"},
				},
			},
			"scalar": {ExposeAs: "scalar"},
		},
	}
	got := projectLegacyRoot(spec, map[string]interface{}{
		"inputDs":  map[string]interface{}{"q": "abc"},
		"outputDs": map[string]interface{}{"items": []interface{}{map[string]interface{}{"id": "a"}}},
		"nested":   []interface{}{"a"},
		"scalar":   "x",
	})
	assert.JSONEq(t, `{"input":{"q":"abc"},"output":{"items":[{"id":"a"}]},"scalar":"x"}`, mustJSON(t, got))

	got = projectLegacyRoot(&Spec{DataSources: map[string]*DataSource{}}, map[string]interface{}{})
	assert.JSONEq(t, `{"input":{},"output":{}}`, mustJSON(t, got))
}

func TestSelectPathAdditionalBranches(t *testing.T) {
	root := map[string]interface{}{"output": []interface{}{1, 2}, "input": map[string]interface{}{"q": "abc"}}
	assert.JSONEq(t, `[1,2]`, mustJSON(t, selectPath("output", root)))
	assert.JSONEq(t, `{"q":"abc"}`, mustJSON(t, selectPath("input", root)))
	assert.Nil(t, selectPath("output.items.bad", root))
	assert.Nil(t, selectPath("output.items[a]", map[string]interface{}{"output": map[string]interface{}{"items": []interface{}{1}}}))
	assert.Nil(t, selectPath("output.items.0", map[string]interface{}{"output": "x"}))
	assert.Equal(t, 2, selectPath("1", []interface{}{1, 2}))
	assert.JSONEq(t, `{"items":[1,2]}`, mustJSON(t, selectPath("output", map[string]interface{}{"items": []interface{}{1, 2}})))
	assert.JSONEq(t, `{"items":[1,2]}`, mustJSON(t, selectPath("input", map[string]interface{}{"items": []interface{}{1, 2}})))
}

func TestCloneJSONLikeAdditionalBranches(t *testing.T) {
	assert.Nil(t, cloneJSONLike(nil))
	ch := make(chan int)
	assert.Equal(t, ch, cloneJSONLike(ch))
	type weird struct{ A int }
	assert.JSONEq(t, `{"A":1}`, mustJSON(t, cloneJSONLike(weird{A: 1})))
	assert.Equal(t, invalidJSONValue{}, cloneJSONLike(invalidJSONValue{}))
}

func TestParsePayloadListAdditionalBranches(t *testing.T) {
	assert.Nil(t, parsePayloadList(nil))
	assert.Nil(t, parsePayloadList([]string{"", "not-json"}))
}

func mustJSON(t *testing.T, value interface{}) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

type invalidJSONValue struct{}

func (invalidJSONValue) MarshalJSON() ([]byte, error) { return []byte("{"), nil }
