package aguistate

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyPatchJSONAllOperationsAndAnyRoot(t *testing.T) {
	initial := []byte(`{"a/b":{"~key":1},"arr":[1,2],"big":9007199254740991}`)
	patch := []byte(`[{"op":"test","path":"/a~1b/~0key","value":1.0},{"op":"add","path":"/arr/-","value":3},{"op":"replace","path":"/a~1b/~0key","value":2},{"op":"copy","from":"/big","path":"/copy"},{"op":"move","from":"/copy","path":"/moved"},{"op":"remove","path":"/arr/0","unused":{"retained":null}}]`)
	result, err := ApplyPatchJSON(initial, patch)
	require.NoError(t, err)
	require.JSONEq(t, `{"a/b":{"~key":2},"arr":[2,3],"big":9007199254740991,"moved":9007199254740991}`, string(result))
	for _, root := range []string{`null`, `false`, `42`, `"scalar"`, `[]`, `{}`} {
		result, err := ApplyPatchJSON([]byte(root), []byte(`[{"op":"replace","path":"","value":"new"},{"op":"test","path":"","value":"new"},{"op":"add","path":"","value":{"next":true}},{"op":"remove","path":""},{"op":"test","path":"","value":null}]`))
		require.NoError(t, err, root)
		require.Equal(t, `null`, string(result))
	}
	result, err = ApplyPatchJSON([]byte(`1e999999999`), []byte(`[{"op":"test","path":"","value":10e999999998}]`))
	require.NoError(t, err)
	require.Equal(t, `1e999999999`, string(result))
}
func TestApplyPatchJSONFailuresAreAtomic(t *testing.T) {
	state := []byte(`{"object":{"x":1},"arr":[1]}`)
	original := append([]byte(nil), state...)
	for _, patch := range []string{
		`[{"op":"add","path":"/ok","value":1},{"op":"test","path":"/object/x","value":2}]`,
		`[{"op":"move","from":"/object","path":"/object/child"}]`,
		`[{"op":"move","from":"","path":"/nested"}]`,
		`[{"op":"remove","path":"/arr/-1"}]`,
		`[{"op":"add","path":"/arr/01","value":2}]`,
		`[{"op":"copy","from":"/missing","path":"/copied"}]`,
	} {
		_, err := ApplyPatchJSON(state, []byte(patch))
		require.Error(t, err, patch)
		require.Equal(t, original, state)
	}
	// Object keys that look like negative indices are ordinary property names.
	result, err := ApplyPatchJSON([]byte(`{"-1":0}`), []byte(`[{"op":"replace","path":"/-1","value":1}]`))
	require.NoError(t, err)
	require.True(t, json.Valid(result))
}
