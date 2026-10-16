package agent

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	registry "github.com/viant/agently-core/internal/tool/registry"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/mcpname"
	svc "github.com/viant/agently-core/protocol/tool/service"
	toolservice "github.com/viant/agently-core/protocol/tool/service/llm/tools"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/toolselection"
	"github.com/viant/agently-core/service/intake"
	uimeta "github.com/viant/mcp-ui/meta"
)

type exposureFixture struct{}

func (*exposureFixture) Name() string { return "exposure" }
func (*exposureFixture) Methods() svc.Signatures {
	return svc.Signatures{{Name: "read", Input: reflect.TypeOf(&struct{}{}), Output: reflect.TypeOf(&struct{}{})}, {Name: "helper", Input: reflect.TypeOf(&struct{}{}), Output: reflect.TypeOf(&struct{}{})}}
}
func (*exposureFixture) Method(string) (svc.Executable, error) {
	return func(context.Context, interface{}, interface{}) error { return nil }, nil
}
func (*exposureFixture) MCPUIToolUI(method string) (uimeta.ToolUI, bool) {
	visibility := []string{"model"}
	if method == "helper" {
		visibility = []string{"app"}
	}
	return uimeta.ToolUI{Visibility: visibility, ResourceUri: "ui://exposure/window"}, true
}

func TestModelHiddenToolCannotEnterIntakeDiscoveryOrActivation(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(&exposureFixture{}))
	require.NoError(t, reg.AddInternalService(toolservice.New()))
	s := &Service{registry: reg}
	for _, binding := range []bool{true, false} {
		t.Run(map[bool]string{true: "binding", false: "resolution"}[binding], func(t *testing.T) {
			ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{TurnID: "turn", ConversationID: "conversation"})
			ctx = toolselection.WithState(ctx, nil)
			in := &QueryInput{Agent: &agentmdl.Agent{Tool: agentmdl.Tool{Items: []*llm.Tool{{Name: "exposure:*"}}}}, Context: map[string]interface{}{intake.ContextKey: &intake.Context{Prompting: intake.PromptingContext{ToolSelection: &intake.ToolSelection{Names: []string{"exposure:read"}, Complete: true}}}}}
			if binding {
				_, err = s.buildToolSignatures(ctx, in)
			} else {
				_, err = s.resolveTools(ctx, in)
			}
			require.NoError(t, err)
			list, err := toolservice.New().Method("list")
			require.NoError(t, err)
			out := &toolservice.ListOutput{}
			require.NoError(t, list(ctx, &toolservice.ListInput{}, out))
			require.Equal(t, []toolselection.Candidate{{Name: mcpname.Canonical("exposure:read"), Description: "read"}}, out.Tools)
			activate, _ := toolservice.New().Method("activate")
			require.Error(t, activate(ctx, &toolservice.ActivateInput{Names: []string{mcpname.Canonical("exposure:helper")}}, &toolservice.ActivateOutput{}))
			require.NoError(t, activate(ctx, &toolservice.ActivateInput{Names: []string{mcpname.Canonical("exposure:read")}}, &toolservice.ActivateOutput{}))
		})
	}
}
