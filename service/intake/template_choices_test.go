package intake

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	tpldef "github.com/viant/agently-core/protocol/template"
	"github.com/viant/agently-core/service/core"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
)

type templateChoiceReader struct {
	templates []*tpldef.Template
	listCalls int
}

func (r *templateChoiceReader) LoadAll(context.Context) ([]*tpldef.Template, error) {
	r.listCalls++
	return r.templates, nil
}

func (*templateChoiceReader) Load(context.Context, string) (*tpldef.Template, error) {
	return nil, errors.New("intake should select metadata without loading a body")
}

type templateChoiceModel struct {
	choice  string
	request *llm.GenerateRequest
}

func (m *templateChoiceModel) Find(context.Context, string) (llm.Model, error) { return m, nil }
func (*templateChoiceModel) Implements(string) bool                            { return false }
func (m *templateChoiceModel) Generate(_ context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.request = request
	raw, err := json.Marshal(map[string]any{"prompting": map[string]any{"templateId": m.choice}})
	if err != nil {
		return nil, err
	}
	return &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.NewAssistantMessage(string(raw))}}}, nil
}

func TestIntakeKeepsPublishedTemplateChoiceIdentity(t *testing.T) {
	const first = "template://examples/summary"
	const second = "template://samples/summary"
	const local = "workspace-notes"
	for _, tt := range []struct {
		name      string
		choice    string
		collision bool
		want      string
	}{
		{name: "published reference", choice: second, collision: true, want: second},
		{name: "unique legacy ID", choice: "summary", want: first},
		{name: "ambiguous legacy ID", choice: "summary", collision: true},
		{name: "workspace ID", choice: local, collision: true, want: local},
		{name: "unlisted reference", choice: "template://other/summary", collision: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reader := &templateChoiceReader{templates: []*tpldef.Template{
				{ID: "summary", Name: "Example summary", Description: "Examples", ResourceURI: first},
				{ID: local, Name: "Workspace notes"},
			}}
			enum := []string{"", first, local}
			if tt.collision {
				reader.templates = append(reader.templates, &tpldef.Template{
					ID: "summary", Name: "Sample summary", Description: "Samples", ResourceURI: second,
				})
				enum = []string{"", first, second, local}
			}
			model := &templateChoiceModel{choice: tt.choice}
			svc := New(core.New(model, nil, nil), WithTemplateRepo(tplrepo.NewWithReader(reader)))
			cfg := &agentmdl.Intake{Enabled: true, Model: "synthetic", Scope: []string{agentmdl.IntakeScopeTemplate}}

			result := svc.Run(t.Context(), "Summarize the records.", cfg, "reader")
			require.NotNil(t, result)
			require.Equal(t, tt.want, result.Prompting.TemplateID)
			require.Equal(t, 1, reader.listCalls)
			require.NotNil(t, model.request)
			require.Contains(t, model.request.Messages[0].Content, "- "+first+": Examples")
			require.Contains(t, model.request.Messages[0].Content, "- "+local+":")
			if tt.collision {
				require.Contains(t, model.request.Messages[0].Content, "- "+second+": Samples")
			}
			props := model.request.Options.OutputSchema["properties"].(map[string]any)
			prompting := props["prompting"].(map[string]any)["properties"].(map[string]any)
			raw, err := json.Marshal(prompting["templateId"].(map[string]any)["enum"])
			require.NoError(t, err)
			var actual []string
			require.NoError(t, json.Unmarshal(raw, &actual))
			require.Equal(t, enum, actual)
		})
	}
}
