package clienttool

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"testing"
)

func TestMapContentPreservesAllPartsAndSourceSemantics(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"first"},{"type":"image","source":{"type":"url","value":"https://example.test/image","mimeType":"image/png"},"metadata":{"detail":"high"}},{"type":"audio","source":{"type":"data","value":"AQID","mimeType":"audio/wav"}},{"type":"video","source":{"type":"file","value":"opaque:handle","provider":"google","mimeType":"video/mp4"}},{"type":"document","source":{"type":"data","value":"AQID","mimeType":"application/pdf"},"metadata":["opaque"]},{"type":"text","text":"last"}]`)
	items, err := MapContent(raw)
	require.NoError(t, err)
	require.Len(t, items, 6)
	require.Equal(t, llm.SourceURL, items[1].Source)
	require.Equal(t, "high", items[1].Metadata["detail"])
	require.Equal(t, llm.SourceBase64, items[2].Source)
	require.Equal(t, llm.SourceFile, items[3].Source)
	require.Equal(t, "opaque:handle", items[3].Data)
	require.Equal(t, "google", items[3].Provider)
	require.Equal(t, llm.ContentTypePDF, items[4].Type)
	require.Equal(t, []interface{}{"opaque"}, items[4].Metadata["ag-ui.partMetadata"])
	require.Equal(t, "firstlast", ContentText(items))
	for _, bad := range []string{`null`, `[{"type":"text"}]`, `[{"type":"image"}]`, `[{"type":"audio","source":{"type":"data","value":"bad","mimeType":"audio/wav"}}]`, `[{"type":"unknown"}]`} {
		_, err := MapContent(json.RawMessage(bad))
		require.Error(t, err)
	}
}

func TestMapContentRetainsExactOpaqueMetadataNumbers(t *testing.T) {
	items, err := MapContent(json.RawMessage(`[{"type":"text","text":"text","metadata":{"counter":9007199254740993,"nested":{"tiny":1.0000000000000000001}}},{"type":"image","source":{"type":"file","value":"opaque","provider":"provider"},"metadata":[9007199254740993,0.0000000000000000001]}]`))
	require.NoError(t, err)
	require.Equal(t, json.Number("9007199254740993"), items[0].Metadata["counter"])
	require.Equal(t, json.Number("1.0000000000000000001"), items[0].Metadata["nested"].(map[string]interface{})["tiny"])
	metadata, err := json.Marshal(items[1].Metadata["ag-ui.partMetadata"])
	require.NoError(t, err)
	require.Equal(t, `[9007199254740993,0.0000000000000000001]`, string(metadata))
}
