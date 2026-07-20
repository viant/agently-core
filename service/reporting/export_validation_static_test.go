package reporting

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateReportFillRequestSupportsStaticDatasets(t *testing.T) {
	require.NoError(t, validateReportFillRequest(json.RawMessage(`{
		"kind":"staticJson",
		"format":"json",
		"rowCount":2,
		"columnKeys":["market","spend"]
	}`), 0))

	err := validateReportFillRequest(json.RawMessage(`{
		"kind":"staticJson",
		"rowCount":2,
		"columnKeys":["market","spend"]
	}`), 0)
	require.EqualError(t, err, "invalid reportFill: missing format")

	err = validateReportFillRequest(json.RawMessage(`{"offset":0}`), 0)
	require.EqualError(t, err, "invalid reportFill: missing limit")
}
