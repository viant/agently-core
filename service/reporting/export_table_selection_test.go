package reporting

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExportTableSelectionUsesOnlyMaterializedAuthoredTable(t *testing.T) {
	raw := json.RawMessage(`{"datasets":[{"request":{"opaque":true}}],"blocks":[{"id":"first","kind":"tableBlock","content":{"value":1}},{"id":"second","kind":"tableBlock","content":{"value":2}},{"id":"chart","kind":"chartBlock"}]}`)
	for _, id := range []string{"", "missing", "chart"} {
		_, err := selectExportTable(raw, id)
		require.Error(t, err)
	}
	selected, err := selectExportTable(raw, "second")
	require.NoError(t, err)
	require.JSONEq(t, `{"datasets":[{"request":{"opaque":true}}],"blocks":[{"id":"second","kind":"tableBlock","content":{"value":2}}]}`, string(selected))
	var request SubmitExportRequest
	require.NoError(t, json.Unmarshal([]byte(`{"tableBlockId":"second","format":"csv"}`), &request))
	require.Equal(t, "second", request.TableBlockID)
	require.Equal(t, "second", cloneSubmitExportRequest(&request).TableBlockID)
}

func TestExportTableSnapshotRetainsPrintOnlyForCompleteSingleTable(t *testing.T) {
	single := json.RawMessage(`{"datasets":[{"rows":[{"value":"alpha"},{"value":"beta"}],"request":{"opaque":true}}], "blocks":[{"id":"table","kind":"tableBlock","content":{"rowCount":2}}]}`)
	print := json.RawMessage(`{"pages":[{"elements":[{"value":"alpha"},{"value":"beta"}]}]}`)
	for _, id := range []string{"", "table"} {
		selected, matchingPrint, err := selectExportTableSnapshot(single, print, id)
		require.NoError(t, err)
		require.Equal(t, single, selected)
		require.Equal(t, print, matchingPrint)
	}
	dashboard := json.RawMessage(`{"datasets":[{"rows":[{"value":"alpha"},{"value":"beta"}],"request":{"opaque":true}}],"blocks":[{"id":"table","kind":"tableBlock","content":{"rowCount":2}},{"id":"chart","kind":"chartBlock"}]}`)
	selected, matchingPrint, err := selectExportTableSnapshot(dashboard, print, "table")
	require.NoError(t, err)
	require.Nil(t, matchingPrint, "a dashboard print must not describe a selected subset")
	require.JSONEq(t, string(single), string(selected))
}
