package template

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	identity "github.com/viant/agently-core/protocol/resource"
	tpldef "github.com/viant/agently-core/protocol/template"
	"github.com/viant/agently-core/runtime/requestctx"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
)

type activationTemplateReader struct {
	catalogTemplates
	failure error
}

func (r *activationTemplateReader) Load(ctx context.Context, key string) (*tpldef.Template, error) {
	if r.failure != nil {
		return nil, r.failure
	}
	return r.catalogTemplates.Load(ctx, key)
}

func TestTemplateActivationKeepsInspectionAndAuthorization(t *testing.T) {
	const uri = "template://example/brief"
	store := convmem.New()
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "conversation", TurnID: "turn"})
	conv := apiconv.NewConversation()
	conv.SetId("conversation")
	require.NoError(t, store.PatchConversations(ctx, conv))
	turn := apiconv.NewTurn()
	turn.SetId("turn")
	turn.SetConversationID("conversation")
	require.NoError(t, store.PatchTurn(ctx, turn))
	reader := &activationTemplateReader{catalogTemplates: catalogTemplates{items: []*tpldef.Template{{
		ID: "brief", Name: "Brief", ResourceURI: uri, Instructions: "Use synthetic evidence.",
		Schema:   map[string]interface{}{"type": "object"},
		Fences:   []tpldef.Fence{{Lang: "json"}},
		Examples: []tpldef.TemplateExample{{Title: "Example"}},
	}}}}
	service := New(tplrepo.NewWithReader(reader), nil, WithConversationClient(store))
	include := false
	args := &GetInput{Name: uri, IncludeDocument: &include}
	var output GetOutput
	require.NoError(t, service.get(ctx, args, &output))
	require.Equal(t, "Use synthetic evidence.", output.Instructions)
	require.NotEmpty(t, output.Fences)
	require.NotEmpty(t, output.Examples)
	require.NotEmpty(t, output.Schema)
	require.False(t, output.IncludedDocument)

	include = true
	reader.failure = identity.ErrResourceDenied
	require.ErrorIs(t, service.get(ctx, args, &GetOutput{}), identity.ErrResourceDenied)
	reader.failure = nil
	require.ErrorContains(t, service.get(ctx, &GetInput{Name: "template://example/absent", IncludeDocument: &include}, &GetOutput{}), "not found")
	before, err := store.GetConversation(ctx, "conversation", apiconv.WithIncludeTranscript(true))
	require.NoError(t, err)
	require.Empty(t, before.GetTranscript()[0].GetMessages())

	require.NoError(t, service.get(ctx, args, &output))
	require.NotEmpty(t, output.MessageID)
	require.Equal(t, GetOutput{Name: "Brief", URI: uri, MessageID: output.MessageID, IncludedDocument: true}, output)
	raw, err := json.Marshal(output)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "Use synthetic evidence.")
	var again GetOutput
	require.NoError(t, service.get(ctx, args, &again))
	require.Equal(t, output, again)
	after, err := store.GetConversation(ctx, "conversation", apiconv.WithIncludeTranscript(true))
	require.NoError(t, err)
	messages := after.GetTranscript()[0].GetMessages()
	require.Len(t, messages, 1)
	require.Equal(t, output.MessageID, messages[0].Id)
	require.Equal(t, uri, *messages[0].ContextSummary)
	require.Contains(t, messages[0].GetContent(), "Output schema:")
}
