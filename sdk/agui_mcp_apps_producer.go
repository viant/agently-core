package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/viant/agently-core/runtime/mcpapps"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

func (c *backendClient) aguiPublishMCPApp(ctx context.Context, app mcpapps.AppBinding, result, args json.RawMessage) error {
	if c == nil || c.streaming == nil {
		return fmt.Errorf("MCP app streaming publisher unavailable")
	}
	turn, ok := requestctx.TurnMetaFromContext(ctx)
	if !ok || turn.ConversationID != app.ThreadID || turn.TurnID == "" {
		return fmt.Errorf("MCP app original turn mismatch")
	}
	event, err := MCPAppsActivity("mcp-app-"+uuid.NewString(), app, result, args)
	if err != nil {
		return err
	}
	return c.streaming.Publish(ctx, &streaming.Event{Type: streaming.EventTypeProtocol, ConversationID: turn.ConversationID, StreamID: turn.ConversationID, TurnID: turn.TurnID, ProtocolEvent: event, CreatedAt: time.Now()})
}
