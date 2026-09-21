package skill

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	conv "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	proto "github.com/viant/agently-core/protocol/skill"
	runtime "github.com/viant/agently-core/runtime"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"testing"
)

func TestMCPActivationModesPreserveOrigin(t *testing.T) {
	for _, mode := range []string{"inline", "fork", "detach"} {
		t.Run(mode, func(t *testing.T) {
			f := &skillMCPFixture{body: fmt.Sprintf("---\nname: review\ndescription: Review\nmetadata:\n  agently-context: %s\n---\nRead carefully.", mode)}
			agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "parent"}, Skills: []string{"*"}}
			id := "parent"
			s := &Service{mcpSource: &skillSourceFixture{f: f}, registry: proto.NewRegistry(), agentFinder: &testFinder{agent: agent}, conv: &testConversationClient{conv: &conv.Conversation{Id: "conversation", AgentId: &id}}}
			ctx := requestctx.WithConversationID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "conversation")
			previous := ExecFn
			defer func() { ExecFn = previous }()
			starts := 0
			ExecFn = func(callCtx context.Context, name string, args map[string]interface{}) (string, error) {
				require.Equal(t, "alice", authctx.EffectiveUserID(callCtx))
				if name == "llm/agents:start" {
					starts++
					child := args["agent"].(*agentmdl.Agent)
					state := args["runtime"].(*runtime.Context)
					require.Contains(t, state.SkillActivation.Name, "skill://github/review~")
					require.Equal(t, []string{state.SkillActivation.Name}, child.Skills)
					require.Contains(t, state.SkillActivation.Body, "configured server \"github\"")
					return `{"conversationId":"child","status":"running"}`, nil
				}
				return `{"conversationId":"child","status":"completed","terminal":true,"message":"done","messageKind":"response"}`, nil
			}
			out := &ActivateOutput{}
			require.NoError(t, s.activate(ctx, &ActivateInput{Name: "github/review", Args: "review"}, out))
			require.Contains(t, out.Name, "skill://github/review~")
			require.Equal(t, mode, out.Mode)
			if mode == "inline" {
				require.Zero(t, starts)
				require.Contains(t, out.Body, "untrusted")
			} else {
				require.Equal(t, 1, starts)
				require.Equal(t, "child", out.ChildConversationID)
			}
		})
	}
}
