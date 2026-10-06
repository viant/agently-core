package conversation

import (
	"bytes"
	"compress/gzip"
	"github.com/stretchr/testify/require"
	convcli "github.com/viant/agently-core/app/store/conversation"
	read "github.com/viant/agently-core/internal/datly/message/read"
	"testing"
)

func TestNativeMessageBridgePreservesEveryPayloadBytes(t *testing.T) {
	for _, compression := range []string{"none", "gzip"} {
		t.Run(compression, func(t *testing.T) {
			plain := []byte("text payload with exact bytes \xff\x00 and media references")
			body := plain
			if compression == "gzip" {
				var buffer bytes.Buffer
				writer := gzip.NewWriter(&buffer)
				_, err := writer.Write(plain)
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				body = buffer.Bytes()
			}
			row := &read.MessageView{MessageToolCall: &read.MessageToolCallView{MessageRequestPayload: &read.MessageRequestPayloadView{Compression: compression, InlineBody: &body}, MessageResponsePayload: &read.MessageResponsePayloadView{Compression: compression, InlineBody: &body}}, ToolMessage: []*read.ToolMessageView{{ToolCall: &read.ToolCallView{RequestPayload: &read.RequestPayloadView{Compression: compression, InlineBody: &body}, ResponsePayload: &read.ResponsePayloadView{Compression: compression, InlineBody: &body}}}}, UserElicitationData: &read.UserElicitationDataView{Compression: compression, InlineBody: &body}, ModelCall: &read.ModelCallView{ModelCallRequestPayload: &read.ModelCallRequestPayloadView{Compression: compression, InlineBody: &body}, ModelCallProviderRequestPayload: &read.ModelCallProviderRequestPayloadView{Compression: compression, InlineBody: &body}, ModelCallResponsePayload: &read.ModelCallResponsePayloadView{Compression: compression, InlineBody: &body}, ModelCallProviderResponsePayload: &read.ModelCallProviderResponsePayloadView{Compression: compression, InlineBody: &body}, ModelCallStreamPayload: &read.ModelCallStreamPayloadView{Compression: compression, InlineBody: &body}}}
			actual, err := decodeNativeMessage(row)
			require.NoError(t, err)
			payloads := []*string{actual.MessageToolCall.MessageRequestPayload.InlineBody, actual.MessageToolCall.MessageResponsePayload.InlineBody, actual.ToolMessage[0].ToolCall.RequestPayload.InlineBody, actual.ToolMessage[0].ToolCall.ResponsePayload.InlineBody, actual.UserElicitationData.InlineBody, actual.ModelCall.ModelCallRequestPayload.InlineBody, actual.ModelCall.ModelCallProviderRequestPayload.InlineBody, actual.ModelCall.ModelCallResponsePayload.InlineBody, actual.ModelCall.ModelCallProviderResponsePayload.InlineBody, actual.ModelCall.ModelCallStreamPayload.InlineBody}
			for _, value := range payloads {
				require.NotNil(t, value)
				require.Equal(t, body, []byte(*value))
				require.Equal(t, string(plain), convcli.DecodeInlineBody(*value, compression))
			}
		})
	}
	require.Nil(t, nativePayloadString(nil))
	empty := []byte{}
	value := nativePayloadString(&empty)
	require.NotNil(t, value)
	require.Empty(t, *value)
}
