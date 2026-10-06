package conversation

import (
	convcli "github.com/viant/agently-core/app/store/conversation"
	read "github.com/viant/agently-core/internal/datly/message/read"
)

// restoreNativePayloadBytes retains the binary payload contract across the
// JSON shape bridge. []byte becomes base64 in JSON; legacy payload strings
// intentionally contain the original bytes for DecodeInlineBody.
func restoreNativePayloadBytes(target *convcli.Message, source *read.MessageView) {
	if source.MessageToolCall != nil && target.MessageToolCall != nil && source.MessageToolCall.MessageRequestPayload != nil && target.MessageToolCall.MessageRequestPayload != nil {
		target.MessageToolCall.MessageRequestPayload.InlineBody = nativePayloadString(source.MessageToolCall.MessageRequestPayload.InlineBody)
	}
	if source.MessageToolCall != nil && target.MessageToolCall != nil && source.MessageToolCall.MessageResponsePayload != nil && target.MessageToolCall.MessageResponsePayload != nil {
		target.MessageToolCall.MessageResponsePayload.InlineBody = nativePayloadString(source.MessageToolCall.MessageResponsePayload.InlineBody)
	}
	if source.UserElicitationData != nil && target.UserElicitationData != nil {
		target.UserElicitationData.InlineBody = nativePayloadString(source.UserElicitationData.InlineBody)
	}
	if source.ModelCall != nil && target.ModelCall != nil && source.ModelCall.ModelCallRequestPayload != nil && target.ModelCall.ModelCallRequestPayload != nil {
		target.ModelCall.ModelCallRequestPayload.InlineBody = nativePayloadString(source.ModelCall.ModelCallRequestPayload.InlineBody)
	}
	if source.ModelCall != nil && target.ModelCall != nil && source.ModelCall.ModelCallProviderRequestPayload != nil && target.ModelCall.ModelCallProviderRequestPayload != nil {
		target.ModelCall.ModelCallProviderRequestPayload.InlineBody = nativePayloadString(source.ModelCall.ModelCallProviderRequestPayload.InlineBody)
	}
	if source.ModelCall != nil && target.ModelCall != nil && source.ModelCall.ModelCallResponsePayload != nil && target.ModelCall.ModelCallResponsePayload != nil {
		target.ModelCall.ModelCallResponsePayload.InlineBody = nativePayloadString(source.ModelCall.ModelCallResponsePayload.InlineBody)
	}
	if source.ModelCall != nil && target.ModelCall != nil && source.ModelCall.ModelCallProviderResponsePayload != nil && target.ModelCall.ModelCallProviderResponsePayload != nil {
		target.ModelCall.ModelCallProviderResponsePayload.InlineBody = nativePayloadString(source.ModelCall.ModelCallProviderResponsePayload.InlineBody)
	}
	if source.ModelCall != nil && target.ModelCall != nil && source.ModelCall.ModelCallStreamPayload != nil && target.ModelCall.ModelCallStreamPayload != nil {
		target.ModelCall.ModelCallStreamPayload.InlineBody = nativePayloadString(source.ModelCall.ModelCallStreamPayload.InlineBody)
	}
	for i, message := range source.ToolMessage {
		if message == nil || message.ToolCall == nil || i >= len(target.ToolMessage) || target.ToolMessage[i] == nil || target.ToolMessage[i].ToolCall == nil {
			continue
		}
		if message.ToolCall.RequestPayload != nil && target.ToolMessage[i].ToolCall.RequestPayload != nil {
			target.ToolMessage[i].ToolCall.RequestPayload.InlineBody = nativePayloadString(message.ToolCall.RequestPayload.InlineBody)
		}
		if message.ToolCall.ResponsePayload != nil && target.ToolMessage[i].ToolCall.ResponsePayload != nil {
			target.ToolMessage[i].ToolCall.ResponsePayload.InlineBody = nativePayloadString(message.ToolCall.ResponsePayload.InlineBody)
		}
	}
}

func nativePayloadString(body *[]uint8) *string {
	if body == nil {
		return nil
	}
	value := string(*body)
	return &value
}
