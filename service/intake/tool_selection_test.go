package intake

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/xeipuuv/gojsonschema"
)

func TestToolSelectionWirePreservesOptionalAndCompleteEmptySemantics(t *testing.T) {
	cases := []struct {
		name, wire string
		want       *ToolSelection
	}{
		{"legacy", `{"prompting":{"appendToolBundles":["required-workflow"]}}`, nil},
		{"null", `{"prompting":{"toolSelection":null}}`, nil},
		{"complete empty", `{"prompting":{"toolSelection":{"names":[],"complete":true}}}`, &ToolSelection{Names: []string{}, Complete: true}},
		{"complete names", `{"prompting":{"toolSelection":{"names":["catalog/read","discovery/search"],"complete":true}}}`, &ToolSelection{Names: []string{"catalog/read", "discovery/search"}, Complete: true}},
		{"incomplete", `{"prompting":{"toolSelection":{"names":["catalog/read"],"complete":false}}}`, &ToolSelection{Names: []string{"catalog/read"}}},
		{"missing completion", `{"prompting":{"toolSelection":{"names":["catalog/read"]}}}`, &ToolSelection{Names: []string{"catalog/read"}}},
		{"missing names not explicit empty", `{"prompting":{"toolSelection":{"complete":true}}}`, &ToolSelection{Names: []string{}}},
		{"null names not explicit empty", `{"prompting":{"toolSelection":{"names":null,"complete":true}}}`, &ToolSelection{Names: []string{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := parseOutput(tc.wire)
			require.NoError(t, err)
			require.Equal(t, tc.want, parsed.Prompting.ToolSelection)
			canonical, err := canonicalIntakeContent(parsed)
			require.NoError(t, err)
			again, err := parseOutput(canonical)
			require.NoError(t, err)
			require.Equal(t, tc.want, again.Prompting.ToolSelection)
			if tc.want != nil {
				var value map[string]any
				require.NoError(t, json.Unmarshal([]byte(canonical), &value))
				selection := value["prompting"].(map[string]any)["toolSelection"].(map[string]any)
				require.IsType(t, []any{}, selection["names"])
				require.Contains(t, selection, "complete")
			}
		})
	}
}

func TestToolSelectionCoexistsWithProfileAndBundleWorkflow(t *testing.T) {
	raw := `{"prompting":{"suggestedProfileId":"analysis","appendToolBundles":["required-workflow"],"templateId":"report","toolSelection":{"names":["catalog/read"],"complete":true}}}`
	parsed, err := parseOutput(raw)
	require.NoError(t, err)
	filterByScope(parsed, &agentmdl.Intake{Scope: []string{"profile", "tools", "template"}})
	canonical, err := canonicalIntakeContent(parsed)
	require.NoError(t, err)
	again, err := parseOutput(canonical)
	require.NoError(t, err)
	require.Equal(t, "analysis", again.Prompting.SuggestedProfileID)
	require.Equal(t, []string{"required-workflow"}, again.Prompting.AppendToolBundles)
	require.Equal(t, "report", again.Prompting.TemplateID)
	require.Equal(t, &ToolSelection{Names: []string{"catalog/read"}, Complete: true}, again.Prompting.ToolSelection)
	filterByScope(again, &agentmdl.Intake{Scope: []string{"profile", "template"}})
	require.Nil(t, again.Prompting.ToolSelection)
	require.Nil(t, again.Prompting.AppendToolBundles)
	require.Equal(t, "analysis", again.Prompting.SuggestedProfileID)
}

func TestToolSelectionSchemaUsesExistingToolsScopeAndConservativeInstructions(t *testing.T) {
	enabled := &agentmdl.Intake{Scope: []string{"tools"}}
	schema := buildOutputJSONSchema(enabled)
	prompting := schema["properties"].(map[string]any)["prompting"].(map[string]any)
	props := prompting["properties"].(map[string]any)
	require.Contains(t, props, "appendToolBundles")
	selection := props["toolSelection"].(map[string]any)
	require.Equal(t, "object", selection["type"])
	require.Equal(t, []string{"names", "complete"}, selection["required"])
	require.Equal(t, false, selection["additionalProperties"])
	require.Equal(t, "array", selection["properties"].(map[string]any)["names"].(map[string]any)["type"])
	require.Equal(t, "boolean", selection["properties"].(map[string]any)["complete"].(map[string]any)["type"])
	require.Contains(t, prompting["required"], "toolSelection", "strict schema requires an explicit initial selection or uncertainty")
	prompt := buildOutputSchema(enabled)
	for _, instruction := range []string{"exact names", "compact capabilities", "complete=false", "names=[]", "appendToolBundles"} {
		require.Contains(t, prompt, instruction)
	}
	require.Contains(t, intakeBasePrompt, "prompting.toolSelection.names")
	disabled := buildOutputJSONSchema(&agentmdl.Intake{Scope: []string{"profile"}})
	disabledPrompting := disabled["properties"].(map[string]any)["prompting"].(map[string]any)
	require.NotContains(t, disabledPrompting["properties"], "toolSelection")
}

func TestToolSelectionCopiesDetachCallerAndValueContext(t *testing.T) {
	original := &Context{Prompting: PromptingContext{ToolSelection: &ToolSelection{Names: []string{"allowed/read"}, Complete: true}}}
	ctx, stored := StoreCallerProvided(nil, original)
	require.Equal(t, SourceCallerProvided, stored.Routing.Source)
	original.Prompting.ToolSelection.Names[0] = "caller mutation"
	original.Prompting.ToolSelection.Complete = false
	require.Equal(t, &ToolSelection{Names: []string{"allowed/read"}, Complete: true}, stored.Prompting.ToolSelection)
	require.Same(t, stored, FromContext(ctx), "stored pointer keeps existing mutable query-context semantics")
	copy := FromContext(map[string]any{ContextKey: *stored})
	copy.Prompting.ToolSelection.Names[0] = "copy mutation"
	copy.Prompting.ToolSelection.Complete = false
	require.Equal(t, &ToolSelection{Names: []string{"allowed/read"}, Complete: true}, stored.Prompting.ToolSelection)
	clone := stored.Prompting.ToolSelection.Clone()
	clone.Names[0] = "clone mutation"
	require.Equal(t, "allowed/read", stored.Prompting.ToolSelection.Names[0])
	var missing *ToolSelection
	require.Nil(t, missing.Clone())
	empty := (&ToolSelection{Complete: true}).Clone()
	require.NotNil(t, empty.Names)
	require.Empty(t, empty.Names)
}

func TestToolSelectionRejectsMalformedWireWithoutTrustingNames(t *testing.T) {
	for _, raw := range []string{`{"prompting":{"toolSelection":{"names":[42],"complete":true}}}`, `{"prompting":{"toolSelection":{"names":[],"complete":"yes"}}}`} {
		_, err := parseOutput(raw)
		require.Error(t, err)
	}
	parsed, err := parseOutput(`{"prompting":{"toolSelection":{"names":["not-authorized/invented"],"complete":true}}}`)
	require.NoError(t, err)
	require.Equal(t, []string{"not-authorized/invented"}, parsed.Prompting.ToolSelection.Names, "parsing records a hint; execution must validate it against the authorized surface")
}

func TestToolSelectionSchemaRestrictsNamesToAdvertisedTools(t *testing.T) {
	svc := &Service{}
	cfg := &agentmdl.Intake{Scope: []string{"tools"}}
	in := svc.buildGenerateInputWithContext(context.Background(), "model", "instructions", "request", "user", cfg,
		WithToolNames([]string{"catalog-read", "llm_skills-activate"}))
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"catalog-read", true},
		{"llm_skills-activate", true},
		{"catalog-workflow", false},
		{"catalog-help", false},
		{"catalog:read", false},
		{"invented-tool", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := map[string]any{"prompting": map[string]any{
				"appendToolBundles": []string{}, "toolSelection": ToolSelection{Names: []string{tc.name}, Complete: true},
			}}
			result, err := gojsonschema.Validate(gojsonschema.NewGoLoader(in.Options.OutputSchema), gojsonschema.NewGoLoader(value))
			require.NoError(t, err)
			require.Equal(t, tc.valid, result.Valid(), "%v", result.Errors())
		})
	}
}

func TestToolSelectionSnapshotAbsentAndEmpty(t *testing.T) {
	svc := &Service{}
	cfg := &agentmdl.Intake{Scope: []string{"tools"}}
	for _, tc := range []struct {
		name  string
		names []string
		valid bool
	}{
		{"absent retains legacy contract", nil, true},
		{"known empty rejects tool names", []string{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := svc.buildOutputJSONSchema(context.Background(), cfg, WithToolNames(tc.names))
			value := map[string]any{"prompting": map[string]any{
				"appendToolBundles": []string{}, "toolSelection": ToolSelection{Names: []string{"catalog-read"}, Complete: true},
			}}
			result, err := gojsonschema.Validate(gojsonschema.NewGoLoader(schema), gojsonschema.NewGoLoader(value))
			require.NoError(t, err)
			require.Equal(t, tc.valid, result.Valid())
			value["prompting"].(map[string]any)["toolSelection"] = ToolSelection{Names: []string{}, Complete: true}
			result, err = gojsonschema.Validate(gojsonschema.NewGoLoader(schema), gojsonschema.NewGoLoader(value))
			require.NoError(t, err)
			require.True(t, result.Valid(), "%v", result.Errors())
		})
	}
}

func TestValidateToolSelectionPreservesWorkflowAndUncertainty(t *testing.T) {
	for _, tc := range []struct {
		name              string
		allowed, selected []string
		complete          bool
		want              *ToolSelection
	}{
		{"exact tool", []string{"catalog-read"}, []string{"catalog-read"}, true, &ToolSelection{Names: []string{"catalog-read"}, Complete: true}},
		{"bundle id is not a tool", []string{"catalog-read"}, []string{"catalog-workflow"}, true, &ToolSelection{Names: []string{}, Complete: false}},
		{"skill id is not a tool", []string{"catalog-read"}, []string{"catalog-help"}, true, &ToolSelection{Names: []string{}, Complete: false}},
		{"mixed batch is not partially accepted", []string{"catalog-read"}, []string{"catalog-read", "invented"}, true, &ToolSelection{Names: []string{}, Complete: false}},
		{"incomplete invalid batch", []string{"catalog-read"}, []string{"invented"}, false, &ToolSelection{Names: []string{}, Complete: false}},
		{"absent metadata preserves legacy", nil, []string{"invented"}, true, &ToolSelection{Names: []string{"invented"}, Complete: true}},
		{"empty metadata rejects names", []string{}, []string{"catalog-read"}, true, &ToolSelection{Names: []string{}, Complete: false}},
		{"explicit empty remains complete", []string{"catalog-read"}, []string{}, true, &ToolSelection{Names: []string{}, Complete: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := &Context{Prompting: PromptingContext{AppendToolBundles: []string{"catalog-workflow"}, ToolSelection: &ToolSelection{Names: tc.selected, Complete: tc.complete}}}
			validateToolSelection(value, tc.allowed)
			require.Equal(t, tc.want, value.Prompting.ToolSelection)
			require.Equal(t, []string{"catalog-workflow"}, value.Prompting.AppendToolBundles)
		})
	}
}
