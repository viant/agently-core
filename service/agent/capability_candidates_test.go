package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	execconfig "github.com/viant/agently-core/app/executor/config"
	baseembed "github.com/viant/agently-core/genai/embedder/provider/base"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	skillproto "github.com/viant/agently-core/protocol/skill"
	"github.com/viant/agently-core/runtime/capability"
	"github.com/viant/agently-core/runtime/toolselection"
	"github.com/viant/agently-core/service/augmenter"
	skillsvc "github.com/viant/agently-core/service/skill"
)

type intakeCandidateEmbedder struct{ failure error }

func (e *intakeCandidateEmbedder) Embed(_ context.Context, texts []string) ([][]float32, int, error) {
	if e.failure != nil {
		return nil, 0, e.failure
	}
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		if strings.Contains(strings.ToLower(text), "calendar") {
			vectors[i] = []float32{1, 0}
		} else {
			vectors[i] = []float32{0, 1}
		}
	}
	return vectors, 0, nil
}

type intakeCandidateFinder struct{ embedder *intakeCandidateEmbedder }

func (f intakeCandidateFinder) Find(context.Context, string) (baseembed.Embedder, error) {
	return f.embedder, nil
}
func (intakeCandidateFinder) Ids() []string { return []string{"fixture"} }

type intakeCandidateSkillDelivery struct{ reads int }

func (*intakeCandidateSkillDelivery) List(context.Context) ([]skillproto.Metadata, error) {
	return []skillproto.Metadata{
		{Name: "review", Description: "Calendar scheduling procedure", URI: "skill://skills/review/SKILL.md", ServerID: "remote"},
		{Name: "retained", Description: "Opaque retained procedure", URI: "skill://skills/retained/SKILL.md", ServerID: "remote", AlwaysInclude: true},
		{Name: "excluded", Description: "Calendar forbidden procedure", URI: "skill://skills/excluded/SKILL.md", ServerID: "remote", AlwaysInclude: true},
	}, nil
}
func (d *intakeCandidateSkillDelivery) Read(context.Context, string) (*skillproto.Skill, bool, error) {
	d.reads++
	return nil, true, errors.New("body read must not occur during metadata ranking")
}
func (d *intakeCandidateSkillDelivery) ReadLocal(context.Context, *skillproto.Skill) (*skillproto.Skill, error) {
	d.reads++
	return nil, errors.New("body read must not occur during metadata ranking")
}

func TestIntakeRanksToolsAndSkillsRetainsExplicitAndAlwaysIncluded(t *testing.T) {
	for _, description := range []string{"", "Workspace capabilities for otherwise ungrouped tasks"} {
		t.Run(description, func(t *testing.T) {
			defs := []llm.ToolDefinition{
				{Name: "north:calendar", Description: "Calendar scheduling tool", SourceResourceURI: "mcp://north/configured", SourceProviderIdentity: "verified-owner", Parameters: map[string]any{"private-schema-marker": true}},
				{Name: "exact:opaque", Description: "Opaque selected operation"},
				{Name: "helpers:retained", Description: "Opaque retained discovery", AlwaysInclude: true},
				{Name: "irrelevant:a", Description: "Unrelated operation"}, {Name: "irrelevant:b", Description: "Unrelated operation"},
			}
			delivery := &intakeCandidateSkillDelivery{}
			skillService := skillsvc.New(nil, nil, nil)
			skillService.SetDelivery(nil, delivery)
			defaults := &execconfig.Defaults{Embedder: "fixture", Discovery: execconfig.DiscoveryDefaults{CandidateLimit: 2, DefaultNamespaceDescription: description}}
			service := &Service{registry: &fakeRegistry{defs: defs}, skillSvc: skillService, defaults: defaults, augmenter: augmenter.New(intakeCandidateFinder{&intakeCandidateEmbedder{}})}
			input := &QueryInput{Query: "calendar and exact:opaque", Agent: &agentmdl.Agent{Skills: []string{"review", "retained"}}}
			summary, names, hints, err := service.intakeCapabilitySnapshot(context.Background(), input)
			require.NoError(t, err)
			require.ElementsMatch(t, []string{"north-calendar", "exact-opaque", "helpers-retained"}, names)
			for _, retained := range []string{"Calendar scheduling tool", "Calendar scheduling procedure", "Opaque retained procedure", "Opaque retained discovery", "Opaque selected operation"} {
				require.Contains(t, summary, retained)
			}
			require.NotContains(t, summary, "Unrelated operation")
			require.NotContains(t, summary, "forbidden")
			require.NotContains(t, summary, "private-schema-marker")
			require.Zero(t, delivery.reads)
			require.Contains(t, hints, "default")
			if description == "" {
				require.Contains(t, summary, capability.DefaultNamespaceDescription)
			} else {
				require.Contains(t, summary, description)
			}
			// Ranking never narrows the authorized turn catalog. A later explicit
			// discovery/activation can still select a candidate outside the first budget.
			ctx := toolselection.WithState(context.Background(), nil)
			definitions, err := service.discoverableToolDefinitions(ctx, input)
			require.NoError(t, err)
			state, _ := toolselection.FromContext(ctx)
			state.SetCandidates(defsToTools(definitions))
			require.NoError(t, state.Activate([]string{"irrelevant-a"}))
		})
	}
}

func TestIntakeEmbeddingFailureRetainsFullAuthorizedMetadata(t *testing.T) {
	service := &Service{registry: &fakeRegistry{defs: []llm.ToolDefinition{{Name: "one:read", Description: "First authored description"}, {Name: "two:read", Description: "Second authored description"}}}, defaults: &execconfig.Defaults{Embedder: "fixture", Discovery: execconfig.DiscoveryDefaults{CandidateLimit: 1}}, augmenter: augmenter.New(intakeCandidateFinder{&intakeCandidateEmbedder{failure: errors.New("synthetic outage")}})}
	summary, names, _, err := service.intakeCapabilitySnapshot(context.Background(), &QueryInput{Query: "request", Agent: &agentmdl.Agent{}})
	require.NoError(t, err)
	require.Len(t, names, 2)
	require.Contains(t, summary, "First authored description")
	require.Contains(t, summary, "Second authored description")
}
