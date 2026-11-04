package conversationaudit

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuditNativeJSONMatchesTableAndCSV(t *testing.T) {
	ctx := principal(context.Background(), "alice")
	s, db := fixture(ctx, t)
	input := "=SUM(1,2)\n" + strings.Repeat("Full input żółć,\"quoted\"\n", 50)
	output := "@original output\n" + strings.Repeat("Full output\n", 50)
	_, err := db.ExecContext(ctx, "UPDATE message SET content=? WHERE id='u'", input)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE message SET content=? WHERE id='final'", output)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE turn SET feedback_rating=-1,feedback_comment='=original comment' WHERE id='t2'")
	require.NoError(t, err)
	// A requested small page does not restrict either full export.
	f := Filter{ConversationID: "own", Limit: 1, IncludeTotal: true}
	data, err := s.JSON(ctx, f)
	require.NoError(t, err)
	var records []map[string]any
	require.NoError(t, json.Unmarshal(data, &records))
	require.Len(t, records, 2)
	r := records[0]
	require.Len(t, r, len(exportHeader))
	for _, key := range exportHeader {
		require.Contains(t, r, key)
	}
	require.Equal(t, input+"\n\nMore detail", r["input"])
	require.Equal(t, output, r["output"])
	require.Equal(t, "=original comment", r["feedback_comment"])
	require.Equal(t, float64(-1), r["feedback_rating"])
	require.Equal(t, float64(40), r["input_tokens"])
	require.Equal(t, float64(60), r["output_tokens"])
	require.Equal(t, []any{"example/model"}, r["models"])
	require.Nil(t, r["execution_span_ms"])
	require.Equal(t, []any{}, records[1]["models"])
	require.Equal(t, "2026-10-26T13:00:00Z", r["turn_created_at"])
	require.Equal(t, "2026-10-25T12:00:00Z", r["conversation_created_at"])
	csvData, err := s.CSV(ctx, f)
	require.NoError(t, err)
	csvRows, err := csv.NewReader(strings.NewReader(string(csvData))).ReadAll()
	require.NoError(t, err)
	require.Equal(t, exportHeader, csvRows[0])
	require.Len(t, csvRows, len(records)+1)
	for index, key := range exportHeader {
		if index > 0 {
			require.Less(t, strings.Index(string(data), `"`+exportHeader[index-1]+`":`), strings.Index(string(data), `"`+key+`":`))
		}
	}
	require.Equal(t, "'"+r["input"].(string), csvRows[1][4])
	require.Equal(t, "'"+output, csvRows[1][5])
	require.Equal(t, "'=original comment", csvRows[1][14])

	data, err = s.JSON(ctx, Filter{ConversationID: "other"})
	require.NoError(t, err)
	require.Equal(t, "[]", string(data), "no other owner's rows can be exported")
	_, err = s.JSON(ctx, Filter{ConversationID: "own", UserID: "bob"})
	require.ErrorIs(t, err, ErrForbidden)
	_, err = s.JSON(ctx, Filter{ConversationID: "own", Cursor: "invalid"})
	require.ErrorIs(t, err, ErrFilter)

	// Incomplete usage is an explicit null, never an invented zero.
	_, err = db.ExecContext(ctx, "UPDATE model_call SET prompt_tokens=NULL WHERE message_id='final'")
	require.NoError(t, err)
	data, err = s.JSON(ctx, Filter{ConversationID: "own"})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &records))
	require.Nil(t, records[0]["input_tokens"])
}
