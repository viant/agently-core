package conversation

import (
	conversationmodel "github.com/viant/agently-core/model/conversation"
	generatedfilemodel "github.com/viant/agently-core/model/generatedfile"
	messagemodel "github.com/viant/agently-core/model/message"
	modelcallmodel "github.com/viant/agently-core/model/modelcall"
	payloadmodel "github.com/viant/agently-core/model/payload"
	toolcallmodel "github.com/viant/agently-core/model/toolcall"
	turnmodel "github.com/viant/agently-core/model/turn"
)

// NewConversation allocates a mutable conversation with Has populated.
func NewConversation() *MutableConversation {
	v := &conversationmodel.Conversation{Has: &conversationmodel.ConversationHas{}}
	return (*MutableConversation)(v)
}

// NewMessage allocates a mutable message with Has populated.
func NewMessage() *MutableMessage {
	v := &messagemodel.Message{Has: &messagemodel.MessageHas{}}
	return (*MutableMessage)(v)
}

// NewModelCall allocates a mutable model call with Has populated.
func NewModelCall() *MutableModelCall {
	v := &modelcallmodel.ModelCall{Has: &modelcallmodel.ModelCallHas{}}
	return (*MutableModelCall)(v)
}

// NewToolCall allocates a mutable tool call with Has populated.
func NewToolCall() *MutableToolCall {
	v := &toolcallmodel.ToolCall{Has: &toolcallmodel.ToolCallHas{}}
	return (*MutableToolCall)(v)
}

// NewPayload allocates a mutable payload with Has populated.
func NewPayload() *MutablePayload {
	v := &payloadmodel.Payload{Has: &payloadmodel.PayloadHas{}}
	return (*MutablePayload)(v)
}

// NewTurn allocates a mutable turn with Has populated.
func NewTurn() *MutableTurn {
	v := &turnmodel.Turn{Has: &turnmodel.TurnHas{}}
	return (*MutableTurn)(v)
}

// NewGeneratedFile allocates a mutable generated file with Has populated.
func NewGeneratedFile() *MutableGeneratedFile {
	v := &generatedfilemodel.GeneratedFile{Has: &generatedfilemodel.GeneratedFileHas{}}
	return (*MutableGeneratedFile)(v)
}
