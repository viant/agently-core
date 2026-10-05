package core

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/runtime/clienttool"
	"testing"
)

func TestExpandedPromptPreservesOrderedProtocolUserMedia(t *testing.T) {
	items, err := clienttool.MapContent(json.RawMessage(`[{"type":"text","text":"before"},{"type":"image","source":{"type":"data","value":"AQID","mimeType":"image/png"}},{"type":"text","text":"after"}]`))
	require.NoError(t, err)
	h := &binding.History{CurrentTurnID: "turn", Current: &binding.Turn{ID: "turn", Messages: []*binding.Message{{ID: "user", Kind: binding.MessageKindChatUser, Role: "user", Content: "beforeafter", ContentItems: items}}}}
	messages := historyLLMMessagesWithExpandedCurrentPrompt(h, "beforeafter", nil, items)
	require.Len(t, messages, 1)
	require.Equal(t, items, messages[0].Items)
	messages = historyLLMMessagesWithExpandedCurrentPrompt(h, "Expanded: beforeafter", nil, items)
	require.Len(t, messages[0].Items, 4)
	require.Equal(t, items, messages[0].Items[1:])
	input := &GenerateInput{Binding: &binding.Binding{}, Message: []llm.Message{{Role: llm.RoleUser, Items: items}}}
	err = (&Service{}).enforceAttachmentPolicy(context.Background(), input, nil)
	require.ErrorContains(t, err, "AG-UI media content unsupported")
}
