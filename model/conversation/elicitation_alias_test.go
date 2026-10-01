package conversation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/internal/datly/codec"
)

var _ *codec.JSON = (*Elicitation)(nil)

func TestElicitationAliasPreservesScanAndValue(t *testing.T) {
	for _, test := range []struct {
		name    string
		input   any
		wire    string
		value   any
		failure string
	}{
		{name: "null", wire: "null"},
		{name: "empty text", input: "", wire: "null"},
		{name: "whitespace", input: []byte("  \t"), wire: "null"},
		{name: "empty object", input: "{}", wire: "{}"},
		{name: "nested data", input: []byte(`{"status":"pending","data":{"MixedCase":7}}`), wire: `{"status":"pending","data":{"MixedCase":7}}`, value: `{"data":{"MixedCase":7},"status":"pending"}`},
		{name: "malformed", input: "{", failure: "unexpected end of JSON input"},
		{name: "unsupported source", input: 7, failure: "unsupported elicitation scan type int"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value Elicitation
			err := value.Scan(test.input)
			if test.failure != "" {
				require.EqualError(t, err, test.failure)
				return
			}
			require.NoError(t, err)
			encoded, err := json.Marshal(value)
			require.NoError(t, err)
			require.JSONEq(t, test.wire, string(encoded))
			got, err := value.Value()
			require.NoError(t, err)
			require.Equal(t, test.value, got)
		})
	}
}
