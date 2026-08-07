package converse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/base"
	mcbuf "github.com/viant/agently-core/service/core/modelcall"
)

const providerName = "bedrock"

func (c *Client) Implements(feature string) bool {
	switch feature {
	case base.CanUseTools, base.CanStream, base.SupportsInstructions:
		return true
	case base.SupportsContextContinuation:
		return false
	}
	return false
}

func (c *Client) AdviseBackoff(err error, _ int) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	var throttling *types.ThrottlingException
	if errors.As(err, &throttling) {
		return 30 * time.Second, true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && strings.Contains(strings.ToLower(apiErr.ErrorCode()), "throttl") {
		return 30 * time.Second, true
	}
	return 0, false
}

func (c *Client) Generate(ctx context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	parts, err := c.validateAndConvert(request)
	if err != nil {
		return nil, err
	}
	input := &bedrockruntime.ConverseInput{ModelId: aws.String(c.Model), Messages: parts.Messages, System: parts.System, InferenceConfig: parts.InferenceConfig, ToolConfig: parts.ToolConfig}
	observer := mcbuf.ObserverFromContext(ctx)
	if observer != nil {
		payload, _ := json.Marshal(request)
		if next, observerErr := observer.OnCallStart(ctx, mcbuf.Info{Provider: providerName, Model: c.Model, ModelKind: "chat", LLMRequest: request, Payload: payload, StartedAt: time.Now()}); observerErr == nil {
			ctx = next
		} else {
			return nil, fmt.Errorf("observer OnCallStart failed: %w", observerErr)
		}
	}
	output, err := c.BedrockClient.Converse(ctx, input)
	if err != nil {
		if observer != nil {
			_ = observer.OnCallEnd(ctx, mcbuf.Info{Provider: providerName, Model: c.Model, ModelKind: "chat", CompletedAt: time.Now(), Err: err.Error()})
		}
		return nil, fmt.Errorf("failed to converse with Bedrock model: %w", err)
	}
	messageOutput, ok := output.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return nil, fmt.Errorf("unsupported Bedrock Converse output %T", output.Output)
	}
	usage := usageFromAWS(output.Usage)
	result := &llm.GenerateResponse{Model: c.Model, Usage: usage, Choices: []llm.Choice{{Index: 0, Message: fromMessage(messageOutput.Value), FinishReason: string(output.StopReason)}}}
	if c.UsageListener != nil && usage != nil && usage.TotalTokens > 0 {
		c.UsageListener.OnUsage(c.Model, usage)
	}
	if observer != nil {
		responseJSON, _ := json.Marshal(result)
		if err := observer.OnCallEnd(ctx, mcbuf.Info{Provider: providerName, Model: c.Model, ModelKind: "chat", ResponseJSON: responseJSON, CompletedAt: time.Now(), Usage: usage, FinishReason: string(output.StopReason), LLMResponse: result}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (c *Client) validateAndConvert(request *llm.GenerateRequest) (*requestParts, error) {
	if strings.TrimSpace(c.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	return toRequest(request, c)
}

func (c *Client) Stream(ctx context.Context, request *llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	parts, err := c.validateAndConvert(request)
	if err != nil {
		return nil, err
	}
	input := &bedrockruntime.ConverseStreamInput{ModelId: aws.String(c.Model), Messages: parts.Messages, System: parts.System, InferenceConfig: parts.InferenceConfig, ToolConfig: parts.ToolConfig}
	observer := mcbuf.ObserverFromContext(ctx)
	if observer != nil {
		payload, _ := json.Marshal(request)
		if next, observerErr := observer.OnCallStart(ctx, mcbuf.Info{Provider: providerName, Model: c.Model, ModelKind: "chat", LLMRequest: request, Payload: payload, StartedAt: time.Now()}); observerErr == nil {
			ctx = next
		} else {
			return nil, fmt.Errorf("observer OnCallStart failed: %w", observerErr)
		}
	}
	output, err := c.BedrockClient.ConverseStream(ctx, input)
	if err != nil {
		if observer != nil {
			_ = observer.OnCallEnd(ctx, mcbuf.Info{Provider: providerName, Model: c.Model, ModelKind: "chat", CompletedAt: time.Now(), Err: err.Error()})
		}
		return nil, fmt.Errorf("failed to stream from Bedrock model: %w", err)
	}
	events := make(chan llm.StreamEvent)
	go c.consumeStream(ctx, output, observer, events)
	return events, nil
}

type pendingTool struct {
	id, name string
	input    strings.Builder
}

type streamState struct {
	tools        map[int32]*pendingTool
	message      llm.Message
	usage        *llm.Usage
	finishReason string
}

func newStreamState() *streamState {
	return &streamState{tools: map[int32]*pendingTool{}, message: llm.Message{Role: llm.RoleAssistant}}
}

func (c *Client) consumeStream(ctx context.Context, output *bedrockruntime.ConverseStreamOutput, observer mcbuf.Observer, events chan<- llm.StreamEvent) {
	defer close(events)
	stream := output.GetStream()
	defer stream.Close()
	state := newStreamState()
	for event := range stream.Events() {
		for _, converted := range applyStreamEvent(event, state) {
			if converted.Kind == llm.StreamEventTextDelta && observer != nil {
				_ = observer.OnStreamDelta(ctx, []byte(converted.Delta))
			}
			events <- converted
		}
	}
	if err := stream.Err(); err != nil {
		events <- llm.StreamEvent{Kind: llm.StreamEventError, Err: err}
		if observer != nil {
			_ = observer.OnCallEnd(ctx, mcbuf.Info{Provider: providerName, Model: c.Model, ModelKind: "chat", CompletedAt: time.Now(), Err: err.Error()})
		}
		return
	}
	if c.UsageListener != nil && state.usage != nil && state.usage.TotalTokens > 0 {
		c.UsageListener.OnUsage(c.Model, state.usage)
	}
	if observer != nil {
		result := &llm.GenerateResponse{Model: c.Model, Usage: state.usage, Choices: []llm.Choice{{Index: 0, Message: state.message, FinishReason: state.finishReason}}}
		responseJSON, _ := json.Marshal(result)
		_ = observer.OnCallEnd(ctx, mcbuf.Info{Provider: providerName, Model: c.Model, ModelKind: "chat", ResponseJSON: responseJSON, CompletedAt: time.Now(), Usage: state.usage, FinishReason: state.finishReason, LLMResponse: result})
	}
}

func applyStreamEvent(event types.ConverseStreamOutput, state *streamState) []llm.StreamEvent {
	switch actual := event.(type) {
	case *types.ConverseStreamOutputMemberMessageStart:
		return []llm.StreamEvent{{Kind: llm.StreamEventTurnStarted, Role: llm.RoleAssistant}}
	case *types.ConverseStreamOutputMemberContentBlockStart:
		start, ok := actual.Value.Start.(*types.ContentBlockStartMemberToolUse)
		if !ok {
			return nil
		}
		index := aws.ToInt32(actual.Value.ContentBlockIndex)
		tool := &pendingTool{id: aws.ToString(start.Value.ToolUseId), name: aws.ToString(start.Value.Name)}
		state.tools[index] = tool
		return []llm.StreamEvent{{Kind: llm.StreamEventToolCallStarted, ToolCallID: tool.id, ToolName: tool.name}}
	case *types.ConverseStreamOutputMemberContentBlockDelta:
		switch delta := actual.Value.Delta.(type) {
		case *types.ContentBlockDeltaMemberText:
			state.message.Content += delta.Value
			return []llm.StreamEvent{{Kind: llm.StreamEventTextDelta, Role: llm.RoleAssistant, Delta: delta.Value}}
		case *types.ContentBlockDeltaMemberToolUse:
			index := aws.ToInt32(actual.Value.ContentBlockIndex)
			if tool := state.tools[index]; tool != nil {
				tool.input.WriteString(aws.ToString(delta.Value.Input))
				return []llm.StreamEvent{{Kind: llm.StreamEventToolCallDelta, ToolCallID: tool.id, ToolName: tool.name, Delta: aws.ToString(delta.Value.Input)}}
			}
		}
	case *types.ConverseStreamOutputMemberContentBlockStop:
		index := aws.ToInt32(actual.Value.ContentBlockIndex)
		tool := state.tools[index]
		if tool == nil {
			return nil
		}
		arguments := map[string]interface{}{}
		if value := strings.TrimSpace(tool.input.String()); value != "" {
			if err := json.Unmarshal([]byte(value), &arguments); err != nil {
				return []llm.StreamEvent{{Kind: llm.StreamEventError, Err: fmt.Errorf("invalid streamed arguments for tool %q: %w", tool.name, err)}}
			}
		}
		state.message.ToolCalls = append(state.message.ToolCalls, llm.ToolCall{ID: tool.id, Name: tool.name, Arguments: arguments, Type: "function"})
		delete(state.tools, index)
		return []llm.StreamEvent{{Kind: llm.StreamEventToolCallCompleted, ToolCallID: tool.id, ToolName: tool.name, Arguments: arguments}}
	case *types.ConverseStreamOutputMemberMetadata:
		state.usage = usageFromAWS(actual.Value.Usage)
		return []llm.StreamEvent{{Kind: llm.StreamEventUsage, Usage: state.usage}}
	case *types.ConverseStreamOutputMemberMessageStop:
		state.finishReason = string(actual.Value.StopReason)
		return []llm.StreamEvent{{Kind: llm.StreamEventTurnCompleted, FinishReason: state.finishReason, Usage: state.usage}}
	}
	return nil
}
