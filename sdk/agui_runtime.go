package sdk

import (
	"context"
	"encoding/json"
	"fmt"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/clienttool"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/shared/toolexec"
)

type aguiRuntime interface {
	aguiStore() aguistore.Store
	aguiResume(context.Context, *agentsvc.QueryInput, string, *agentsvc.QueryOutput) error
	aguiCompleteTool(context.Context, clienttool.PendingCall, json.RawMessage, string) error
}

func (c *backendClient) aguiStore() aguistore.Store { return aguistore.New(c.goalInvoker) }
func (c *backendClient) aguiResume(ctx context.Context, input *agentsvc.QueryInput, turnID string, output *agentsvc.QueryOutput) error {
	return c.agent.ResumeContinuation(ctx, input, turnID, 0, output)
}
func (c *backendClient) aguiCompleteTool(ctx context.Context, call clienttool.PendingCall, content json.RawMessage, toolError string) error {
	return toolexec.CompleteClientToolResult(ctx, c.conv, call, content, toolError)
}

func aguiClientToolSession(input *agui.RunAgentInput) (*clienttool.Session, error) {
	definitions := make([]llm.ToolDefinition, 0, len(input.Tools))
	for _, tool := range input.Tools {
		var parameters map[string]any
		if len(tool.Parameters) > 0 {
			if err := decodeAGUIValue(tool.Parameters, &parameters); err != nil {
				var boolean bool
				if err = json.Unmarshal(tool.Parameters, &boolean); err != nil {
					return nil, fmt.Errorf("invalid client tool parameter schema: %w", err)
				}
				parameters = map[string]any{}
				if !boolean {
					parameters["not"] = map[string]any{}
				}
			}
		}
		definitions = append(definitions, llm.ToolDefinition{Name: tool.Name, Description: tool.Description, Parameters: parameters})
	}
	session, err := clienttool.NewSession(definitions)
	if err != nil {
		return nil, err
	}
	for _, tool := range input.Tools {
		if len(tool.Metadata) > 0 {
			if err = session.SetMetadata(tool.Name, tool.Metadata); err != nil {
				return nil, err
			}
		}
	}
	return session, nil
}
