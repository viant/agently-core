package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	model "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/service/intake"
)

func TestModelDirectActionPreservesNativeResourceOpen(t *testing.T) {
	config := &model.Intake{ModelDirectAction: &model.ModelDirectActionPolicy{AllowedTools: []string{"ui/view:open"}, RequiredProfileID: "workspace_ui", MinConfidence: 0.9}}
	for _, uri := range []string{"report://example/delivery", "window://another/details"} {
		action := intake.NormalizeClassifierDirectAction(&intake.DirectActionContext{ToolName: "ui/view:open", AssistantText: "Opening the selected resource.", Input: map[string]interface{}{"resource": map[string]interface{}{"uri": uri}, "parameters": map[string]interface{}{"entityId": 42}}})
		context := &intake.Context{Classification: intake.ClassificationContext{Confidence: 0.99}, Prompting: intake.PromptingContext{SuggestedProfileID: "workspace_ui"}, DirectAction: action}
		require.True(t, allowModelDirectAction(context, config), "native resource opens must survive both classifier and agent normalization")
		require.Equal(t, uri, context.DirectAction.Input["resource"].(map[string]interface{})["uri"])
	}
}
