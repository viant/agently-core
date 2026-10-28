package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/intake"
	"github.com/viant/jsonrpc"
)

func TestDeniedIntakeCapabilitiesIdentifySourceAndStageWithoutPayload(t *testing.T) {
	t.Setenv("AGENTLY_DEBUG", "off")
	service, input, _, _ := preparedDiscoveryFixture()
	input.ConversationID, input.MessageID = "synthetic-conversation", "synthetic-turn"
	denied := &jsonrpc.Error{Code: -32003, Message: "private-error-message", Data: json.RawMessage(`{"status":403,"token":"private-token"}`)}
	service.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) { return nil, denied }
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{TurnID: input.MessageID, ConversationID: input.ConversationID})
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	_, err := service.intakeCapabilitySummary(ctx, input)
	require.ErrorIs(t, err, denied)
	logs := output.String()
	require.Contains(t, logs, "intake.capabilities.failed source=tools")
	require.Contains(t, logs, "binding.tool_stage.failed")
	require.Contains(t, logs, `stage="candidate_bundles"`)
	require.Contains(t, logs, `convo="synthetic-conversation"`)
	require.Contains(t, logs, "rpcCode=-32003 rpcStatus=403")
	require.NotContains(t, logs, "private-error-message")
	require.NotContains(t, logs, "private-token")
}

func TestDeniedToolCatalogIdentifiesPatternWithoutSelector(t *testing.T) {
	t.Setenv("AGENTLY_DEBUG", "off")
	denied := &jsonrpc.Error{Code: -32003, Message: "private-error-message", Data: json.RawMessage(`{"status":403,"token":"private-token"}`)}
	registry := &selectedSchemaRegistry{fakeRegistry: fakeRegistry{defs: []llm.ToolDefinition{{Name: "remote:query"}}}, broken: denied}
	service := &Service{registry: registry}
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{TurnID: "synthetic-turn", ConversationID: "synthetic-conversation"})
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	_, err := service.cachedBackendToolDefinitions(ctx, "remote:query|root=private-selector")
	require.ErrorIs(t, err, denied)
	logs := output.String()
	require.Contains(t, logs, `pattern="remote:query"`)
	require.Contains(t, logs, "rpcCode=-32003 rpcStatus=403")
	require.NotContains(t, logs, "private-selector")
	require.NotContains(t, logs, "private-error-message")
	require.NotContains(t, logs, "private-token")
}

func TestDeniedBindingIdentifiesPreparationStage(t *testing.T) {
	t.Setenv("AGENTLY_DEBUG", "off")
	denied := &jsonrpc.Error{Code: -32003, Message: "private-error-message", Data: json.RawMessage(`{"status":403,"token":"private-token"}`)}
	service := selectionService()
	service.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) { return nil, denied }
	input := selectionInput(&intake.ToolSelection{Names: []string{"catalog:read"}, Complete: true})
	input.ConversationID, input.MessageID = "synthetic-conversation", "synthetic-turn"
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	_, err := service.buildToolSignatures(context.Background(), input)
	require.ErrorIs(t, err, denied)
	logs := output.String()
	require.Contains(t, logs, `stage="retained_resolution"`)
	require.Contains(t, logs, `stage="bundle_inventory"`)
	require.Contains(t, logs, "rpcCode=-32003 rpcStatus=403")
	require.NotContains(t, logs, "private-error-message")
	require.NotContains(t, logs, "private-token")
}
