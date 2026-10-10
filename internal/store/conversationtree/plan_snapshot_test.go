package conversationtree

import (
	"testing"

	"github.com/stretchr/testify/require"
	fileread "github.com/viant/agently-core/internal/datly/generatedfile/read"
	msgread "github.com/viant/agently-core/internal/datly/message/read"
	modelread "github.com/viant/agently-core/internal/datly/modelcall/read"
	toolread "github.com/viant/agently-core/internal/datly/toolcall/read"
)

func TestPlanPayloadIDsCoverEveryReferenceWithoutBodies(t *testing.T) {
	ptr := func(v string) *string { return &v }
	plan := &DeletePlan{
		Messages:       []*msgread.MessageView{nil, {AttachmentPayloadId: ptr("attachment"), ElicitationPayloadId: ptr("elicitation")}},
		ModelCalls:     []*modelread.ModelCallView{nil, {RequestPayloadId: ptr("request"), ResponsePayloadId: ptr("response"), ProviderRequestPayloadId: ptr("provider-request"), ProviderResponsePayloadId: ptr("provider-response"), StreamPayloadId: ptr("stream")}},
		ToolCalls:      []*toolread.ToolCallView{nil, {RequestPayloadId: ptr("request"), ResponsePayloadId: ptr("tool-response")}},
		GeneratedFiles: []*fileread.GeneratedFileView{nil, {PayloadId: ptr("file")}, {PayloadId: ptr("")}},
	}
	require.Equal(t, []string{"attachment", "elicitation", "file", "provider-request", "provider-response", "request", "response", "stream", "tool-response"}, payloadIDsFromPlan(plan))
	require.Empty(t, payloadIDsFromPlan(nil))
}
