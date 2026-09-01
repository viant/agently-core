package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/auth/mcpauth"
	"github.com/viant/agently-core/protocol/agent/execution"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/elicitation"
	"github.com/viant/mcp-protocol/schema"
)

const defaultMCPAuthWait = 5 * time.Minute

type mcpAuthBlocker struct {
	elicitation *elicitation.Service
}

func (b *mcpAuthBlocker) AwaitMCPAuth(ctx context.Context, required *mcpauth.LinkRequiredError) error {
	if b == nil || b.elicitation == nil || required == nil {
		return fmt.Errorf("mcp oauth interaction is unavailable")
	}
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if !ok || strings.TrimSpace(turn.ConversationID) == "" {
		return fmt.Errorf("mcp oauth interaction requires an active conversation")
	}
	waitFor := required.AuthTimeout
	if waitFor <= 0 {
		waitFor = defaultMCPAuthWait
	}
	waitCtx, cancel := context.WithTimeout(ctx, waitFor)
	defer cancel()
	request := &execution.Elicitation{}
	request.Message = "Connect the required provider to continue."
	request.Mode = schema.ElicitRequestParamsMode("mcp_oauth")
	request.Url = required.ConnectURL()
	_, action, _, err := b.elicitation.Elicit(waitCtx, &turn, "control", request)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(action), "accept") {
		return fmt.Errorf("mcp oauth interaction was not completed")
	}
	return nil
}
