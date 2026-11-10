package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/intent"
	resource "github.com/viant/agently-core/protocol/resource"
	tpl "github.com/viant/agently-core/protocol/template"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/reactor"
	intakerepo "github.com/viant/agently-core/workspace/repository/intake"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
)

type selectedTemplateProfileReader struct {
	reads int
}

func (r *selectedTemplateProfileReader) LoadAll(context.Context) ([]*intent.Profile, error) {
	panic("selected template must not list profiles")
}

func (r *selectedTemplateProfileReader) Load(_ context.Context, id string) (*intent.Profile, error) {
	r.reads++
	if id != "example" || r.reads > 1 {
		return nil, resource.ErrResourceDenied
	}
	return &intent.Profile{ID: id, ResourceURI: "playbook://team/example", ResourceProviderIdentity: "example-provider"}, nil
}

type selectedTemplateReader struct {
	reads int
}

func (r *selectedTemplateReader) LoadAll(context.Context) ([]*tpl.Template, error) {
	panic("selected template must not list templates")
}

func (r *selectedTemplateReader) Load(context.Context, string) (*tpl.Template, error) {
	return nil, resource.ErrResourceDenied
}

func (r *selectedTemplateReader) LoadFrom(_ context.Context, id, parent, provider string) (*tpl.Template, error) {
	if id != "example-output" || parent != "playbook://team/example" || provider != "example-provider" {
		return nil, resource.ErrResourceDenied
	}
	r.reads++
	return &tpl.Template{
		ID: id, Name: id, Instructions: "Return the example field.",
		Schema: map[string]any{"type": "object", "required": []any{"example"}},
	}, nil
}

func TestRunPlanLoopUsesAdmittedTemplateForOutputSchema(t *testing.T) {
	profiles := &selectedTemplateProfileReader{}
	templates := &selectedTemplateReader{}
	convClient := newLoopConvClient(&apiconv.Conversation{
		Id:         "conv-1",
		Transcript: []*conversationmodel.TranscriptView{{Id: "turn-1", ConversationId: "conv-1"}},
	})
	finder := &terminalCarrierCaptureFinder{}
	llmSvc := core.New(finder, nil, convClient)
	svc := &Service{
		llm:          llmSvc,
		conversation: convClient,
		orchestrator: reactor.New(llmSvc, nil, convClient, nil, nil),
		defaults:     &config.Defaults{},
		promptRepo:   intakerepo.NewWithReader(profiles),
		templateRepo: tplrepo.NewWithReader(templates),
	}
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	input := &QueryInput{
		ConversationID: "conv-1",
		UserId:         "user-1",
		Query:          "Render example.",
		TemplateId:     "example-output",
		PlaybookId:     "example",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "simple"},
			ModelSelection: llm.ModelSelection{
				Model: "mock-model", Options: &llm.Options{MaxTokens: 64},
			},
			Prompt: &binding.Prompt{Text: "You are helpful."},
		},
	}

	output := &QueryOutput{}
	require.NoError(t, svc.runPlanLoopFrom(ctx, input, output, planLoopStart{Iteration: 2}))
	require.Equal(t, "final answer", output.Content)
	require.EqualValues(t, 1, finder.calls.Load())
	require.Equal(t, 1, profiles.reads)
	require.Equal(t, 1, templates.reads)
	require.NotNil(t, finder.request)
	require.NotNil(t, finder.request.Options)
	require.True(t, finder.request.Options.JSONMode)
	require.Equal(t, "application/json", finder.request.Options.ResponseMIMEType)
	require.Equal(t, map[string]any{"type": "object", "required": []any{"example"}}, finder.request.Options.OutputSchema)
	require.Equal(t, 64, finder.request.Options.MaxTokens)
	hasTemplate := false
	for _, msg := range finder.request.Messages {
		if strings.Contains(msg.Content, "Return the example field.") {
			hasTemplate = true
			break
		}
	}
	require.True(t, hasTemplate, "the same admitted template provides the prompt instructions")

	err := svc.runPlanLoopFrom(ctx, input, &QueryOutput{}, planLoopStart{Iteration: 3})
	require.ErrorIs(t, err, resource.ErrResourceDenied)
	require.Equal(t, 2, profiles.reads)
	require.Equal(t, 1, templates.reads)
	require.EqualValues(t, 1, finder.calls.Load(), "a later binding must still respect current profile admission")
}
