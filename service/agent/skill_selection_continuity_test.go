package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	execconfig "github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	"github.com/viant/agently-core/internal/toolvalidate"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/intent"
	skillproto "github.com/viant/agently-core/protocol/skill"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolservice "github.com/viant/agently-core/protocol/tool/service/llm/tools"
	agruntime "github.com/viant/agently-core/runtime"
	"github.com/viant/agently-core/runtime/toolselection"
	"github.com/viant/agently-core/service/intake"
	skillsvc "github.com/viant/agently-core/service/skill"
)

// Exercise the same selection -> skill activation -> final binding boundary
// with neutral tools. A skill must not erase an independently selected schema.
func TestInlineSkillPreservesSelectedToolWithoutExposingWholeCatalog(t *testing.T) {
	for _, source := range []string{"intake", "discovery"} {
		t.Run(source, func(t *testing.T) {
			s, ctx, _ := discoveryService(t)
			root := t.TempDir()
			dir := filepath.Join(root, "format-result")
			require.NoError(t, os.MkdirAll(dir, 0755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: format-result\ndescription: Format a result.\nallowed-tools: operations:configure\nmetadata:\n  agently-context: inline\n---\nFormat the requested result.\n"), 0644))
			s.defaults = &execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{root}}}
			store := convmem.New()
			seedConversation(t, store, "conversation")
			message := apiconv.NewMessage()
			message.SetId("question")
			message.SetConversationID("conversation")
			message.SetTurnID("turn")
			message.SetRole("user")
			message.SetType("text")
			message.SetContent("Compare the inventory and format the result.")
			require.NoError(t, store.PatchMessage(ctx, message))
			s.conversation = store
			s.skillSvc = skillsvc.New(s.defaults, store, nil)
			s.skillSvc.SetToolRegistry(s.registry)
			require.NoError(t, s.skillSvc.Load(ctx))
			in := selectionInput(&intake.ToolSelection{Names: []string{"operations:report"}, Complete: true})
			in.ConversationID, in.MessageID = "conversation", "turn"
			in.Agent.Skills = []string{"format-result"}
			if source == "discovery" {
				in.Context[intake.ContextKey].(*intake.Context).Prompting.ToolSelection.Names = []string{"operations:configure"}
				before, err := s.BuildBinding(ctx, in)
				require.NoError(t, err)
				require.NotContains(t, signatureNames(before.Tools.Signatures), "operations-report")
				list, err := toolservice.New().Method("list")
				require.NoError(t, err)
				out := &toolservice.ListOutput{}
				require.NoError(t, list(ctx, &toolservice.ListInput{Query: "operations-report"}, out))
				require.Len(t, out.Tools, 1)
			}
			in.Runtime = &agruntime.Context{SkillActivation: &skillproto.ActivationContext{Name: "format-result", Body: "Format the requested result.", Mode: "inline"}}
			after, err := s.BuildBinding(ctx, in)
			require.NoError(t, err)
			names := signatureNames(after.Tools.Signatures)
			require.Contains(t, names, "operations-report")
			require.NotContains(t, names, "operations-otherReport")
			require.NotContains(t, names, "operations-unbundled")
			require.NotContains(t, names, "operations-appOnly")
			require.True(t, approvalqueue.RequiresQueue(ctx, "operations-report"))
			execution := skillsvc.WithRuntimeState(ctx, s.skillSvc, in.Agent, []string{"format-result"})
			execution = tool.WithResolvedToolDefinitions(execution, after.Tools.Signatures)
			require.NoError(t, toolvalidate.ValidateExecution(execution, nil, "operations-report", nil))
			require.Error(t, toolvalidate.ValidateExecution(execution, &tool.Policy{BlockList: []string{"operations-report"}}, "operations-report", nil))
			// An explicit caller constraint is distinct from inline skill guidance.
			explicit := skillsvc.WithConstraints(execution, &skillsvc.Constraints{ToolPatterns: []string{"operations:configure"}})
			require.Error(t, toolvalidate.ValidateExecution(explicit, nil, "operations-report", nil))
			// Refreshing the caller's scope must still remove a formerly selected tool.
			in.ToolsAllowed = []string{"operations:configure"}
			revoked, err := s.BuildBinding(ctx, in)
			require.NoError(t, err)
			require.NotContains(t, signatureNames(revoked.Tools.Signatures), "operations-report")
			revokedExecution := tool.WithResolvedToolDefinitions(execution, revoked.Tools.Signatures)
			require.Error(t, toolvalidate.ValidateExecution(revokedExecution, nil, "operations-report", nil))
		})
	}
}

// Only independently selected or required tools survive same-service skill
// narrowing. Ordinary base tools must remain subject to the skill's guidance.
func TestInlineSkillNarrowsDefaultsWhileRetainingIndependentSelections(t *testing.T) {
	for _, source := range []string{"default", "intake", "incomplete_intake", "invalid_intake", "discovery", "profile_tool", "profile_bundle", "runtime_tool", "runtime_bundle"} {
		t.Run(source, func(t *testing.T) {
			s := selectionService()
			root := t.TempDir()
			dir := filepath.Join(root, "format-result")
			require.NoError(t, os.MkdirAll(dir, 0755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: format-result\ndescription: Format a result.\nallowed-tools: catalog:read\nmetadata:\n  agently-context: inline\n---\nFormat the result.\n"), 0644))
			s.defaults = &execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{root}}}
			s.skillSvc = skillsvc.New(s.defaults, nil, nil)
			s.skillSvc.SetToolRegistry(s.registry)
			ctx := context.Background()
			require.NoError(t, s.skillSvc.Load(ctx))
			in := selectionInput(nil)
			in.Agent.Skills = []string{"format-result"}
			in.Runtime = &agruntime.Context{SkillActivation: &skillproto.ActivationContext{Name: "format-result", Body: "Format the result.", Mode: "inline"}}
			available := []llm.ToolDefinition{
				{Name: "catalog:read"}, {Name: "catalog:write"}, {Name: "catalog:extra"},
				{Name: "catalog:critical", Tier: llm.ToolTierCritical}, {Name: "catalog:retained", AlwaysInclude: true},
			}
			s.registry.(*fakeRegistry).defs = append(append([]llm.ToolDefinition(nil), available...), llm.ToolDefinition{Name: "catalog:unrelated"})
			wantWrite := true
			switch source {
			case "default":
				wantWrite = false
			case "intake", "incomplete_intake", "invalid_intake":
				selection := &intake.ToolSelection{Names: []string{"catalog:write"}, Complete: source != "incomplete_intake"}
				if source == "invalid_intake" {
					selection.Names = append(selection.Names, "missing:tool")
				}
				in.Context[intake.ContextKey].(*intake.Context).Prompting.ToolSelection = selection
				wantWrite = source == "intake"
			case "discovery":
				ctx = toolselection.WithState(ctx, defsToTools(available))
				state, _ := toolselection.FromContext(ctx)
				require.NoError(t, state.Activate([]string{"catalog:write"}))
			case "profile_tool":
				ctx = withSelectedPromptProfile(ctx, &intent.Profile{PreferredTools: []string{"catalog:write", "catalog:unrelated"}})
			case "profile_bundle":
				ctx = withSelectedPromptProfile(ctx, &intent.Profile{ToolBundles: []string{"workflow"}})
			case "runtime_tool":
				in.ToolsAllowed = []string{"catalog:write"}
			case "runtime_bundle":
				in.ToolBundles = []string{"workflow"}
			}
			b := &binding.Binding{}
			b.Tools.Signatures = toolDefinitionPointers(available)
			require.NoError(t, s.applyActiveSkillToolSurface(ctx, in, b))
			names := signatureNames(b.Tools.Signatures)
			require.Contains(t, names, "catalog:read")
			if wantWrite {
				require.Contains(t, names, "catalog:write")
			} else {
				require.NotContains(t, names, "catalog:write")
			}
			require.Contains(t, names, "catalog:critical")
			require.Contains(t, names, "catalog:retained")
			require.NotContains(t, names, "catalog:extra")
			require.NotContains(t, names, "catalog:unrelated", "preservation must not inject unrelated catalog definitions")
		})
	}
}
