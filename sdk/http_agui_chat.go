package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/viant/agently-core/protocol/agui"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type PreparedAGUIChat struct {
	ConversationID string
	Input          agui.RunAgentInput
}
type AGUIRunOutcome struct {
	Type               string               `json:"type"`
	Interrupts         []agui.WireInterrupt `json:"interrupts,omitempty"`
	PendingToolCallIDs []string             `json:"pendingToolCallIds,omitempty"`
}
type AGUIRunResult struct {
	ThreadID, RunID, ConversationID, TurnID, Content string
	Outcome                                          AGUIRunOutcome
	Usage                                            json.RawMessage
	LastEventID                                      string
}
type AGUIRunError struct{ RunID, Code, Message string }

func (e *AGUIRunError) Error() string {
	return fmt.Sprintf("AG-UI run failed (%s): %s", e.Code, e.Message)
}

type AGUIInterruptError struct{ Result *AGUIRunResult }

func (e *AGUIInterruptError) Error() string {
	return "AG-UI run is waiting for an explicit interrupt response"
}

// PrepareAGUIChat uses authenticated native metadata solely for the internal ↔
// wire thread mapping. Execution/history/state remain owned by AG-UI admission.
func (c *HTTPClient) PrepareAGUIChat(ctx context.Context, input *agentsvc.QueryInput) (*PreparedAGUIChat, error) {
	if input == nil {
		return nil, fmt.Errorf("query input required")
	}
	supported := map[string]bool{"ConversationID": true, "ConversationTitle": true, "ParentConversationID": true, "MessageID": true, "AgentID": true, "Query": true, "DisplayQuery": true, "ResourceURIs": true, "Attachments": true, "ModelOverride": true, "ToolsAllowed": true, "ToolBundles": true, "AutoSelectTools": true, "Context": true, "ElicitationMode": true, "AutoSummarize": true, "AllowedChains": true, "DisableChains": true, "ToolCallExposure": true, "ReasoningEffort": true}
	value := reflect.ValueOf(input).Elem()
	kind := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := kind.Field(i)
		if field.PkgPath != "" || value.Field(i).IsZero() {
			continue
		}
		if !supported[field.Name] {
			return nil, fmt.Errorf("query field %s is not an outward AG-UI control", field.Name)
		}
	}
	if input.ElicitationMode != "" && input.ElicitationMode != "deferred" {
		return nil, fmt.Errorf("AG-UI elicitation is resumed through explicit interrupt responses")
	}
	conversationID := input.ConversationID
	if conversationID == "" {
		conversation, err := c.CreateConversation(ctx, &CreateConversationInput{AgentID: input.AgentID, Title: input.ConversationTitle, ParentConversationID: input.ParentConversationID})
		if err != nil {
			return nil, err
		}
		if conversation == nil || conversation.Id == "" {
			return nil, fmt.Errorf("conversation creation returned no identity")
		}
		conversationID = conversation.Id
	} else if input.ParentConversationID != "" || input.ConversationTitle != "" {
		return nil, fmt.Errorf("existing conversation ownership/title cannot be changed by chat")
	}
	var metadata struct {
		ID       string  `json:"id"`
		ThreadID *string `json:"aguiThreadId"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.conversationsPath+"/"+url.PathEscape(conversationID), nil, &metadata); err != nil {
		return nil, err
	}
	if metadata.ID != conversationID {
		return nil, fmt.Errorf("conversation metadata identity mismatch")
	}
	threadID := conversationID
	if metadata.ThreadID != nil {
		if *metadata.ThreadID == "" {
			return nil, fmt.Errorf("empty wire thread mapping")
		}
		threadID = *metadata.ThreadID
	}
	serverState := true
	payload := &agui.ExecutionPayload{AgentID: input.AgentID, Model: input.ModelOverride, BackendTools: append([]string(nil), input.ToolsAllowed...), ToolBundles: append([]string(nil), input.ToolBundles...), AutoSelectTools: input.AutoSelectTools, AutoSummarize: input.AutoSummarize, AllowedChains: append([]string(nil), input.AllowedChains...), ResourceURIs: append([]string(nil), input.ResourceURIs...), UseServerState: &serverState, ReasoningEffort: input.ReasoningEffort}
	if input.DisableChains {
		yes := true
		payload.DisableChains = &yes
	}
	if input.ToolCallExposure != nil {
		exposure := string(*input.ToolCallExposure)
		payload.ToolCallExposure = &exposure
	}
	if input.DisplayQuery != "" {
		display := input.DisplayQuery
		payload.DisplayQuery = &display
	}
	var err error
	if input.Context != nil {
		payload.Context, err = json.Marshal(input.Context)
		if err != nil {
			return nil, err
		}
	}
	for _, attachment := range input.Attachments {
		if attachment == nil {
			return nil, fmt.Errorf("nil attachment")
		}
		payload.Attachments = append(payload.Attachments, agui.AttachmentRef{Name: attachment.Name, URI: attachment.URI, Mime: attachment.Mime, StagingFolder: attachment.StagingFolder})
	}
	messageID := input.MessageID
	if messageID == "" {
		messageID = uuid.NewString()
	}
	runID := uuid.NewString()
	content, _ := json.Marshal(input.Query)
	forwarded, err := json.Marshal(agui.ForwardedProps{Agently: &agui.Extension{Version: "1", Operation: "chat", RequestID: runID, Payload: payload}})
	if err != nil {
		return nil, err
	}
	request := agui.RunAgentInput{ThreadID: threadID, RunID: runID, Messages: []agui.Message{{ID: messageID, Role: "user", Content: content}}, ForwardedProps: forwarded}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if err = agui.ValidateInput(body); err != nil {
		return nil, err
	}
	return &PreparedAGUIChat{ConversationID: conversationID, Input: request}, nil
}

// CollectAGUI retains only text emitted by this root run, never an older reply
// from the bootstrap snapshot. Every full event remains available to onEvent.
func CollectAGUI(stream *AGUIRunStream, conversationID string, onEvent func(AGUIEvent) error) (*AGUIRunResult, error) {
	if stream == nil {
		return nil, fmt.Errorf("AG-UI stream required")
	}
	result := &AGUIRunResult{ThreadID: stream.ThreadID, RunID: stream.RunID, ConversationID: conversationID}
	texts := map[string]*strings.Builder{}
	order := []string{}
	for {
		event, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil, stream.observationError(io.ErrUnexpectedEOF)
			}
			return nil, err
		}
		var frame struct {
			Type, ThreadID, RunID, MessageID, Role, Delta, Message, Code string
			SubagentRunID                                                *string         `json:"subagentRunId"`
			Outcome                                                      AGUIRunOutcome  `json:"outcome"`
			Usage                                                        json.RawMessage `json:"usage"`
			Metadata                                                     struct {
				Agently struct {
					IdentityVersion string `json:"identityVersion"`
					NativeTurnID    string `json:"nativeTurnId"`
				}
			} `json:"metadata"`
		}
		if err = event.Decode(&frame); err != nil {
			return nil, stream.observationError(err)
		}
		root := frame.SubagentRunID == nil && (frame.RunID == "" || frame.RunID == stream.RunID)
		if root {
			switch frame.Type {
			case "RUN_STARTED":
				if frame.ThreadID != stream.ThreadID || frame.RunID != stream.RunID {
					return nil, stream.observationError(fmt.Errorf("root run identity mismatch"))
				}
				if frame.Metadata.Agently.IdentityVersion == "1" {
					result.TurnID = frame.Metadata.Agently.NativeTurnID
				}
			case "TEXT_MESSAGE_START":
				if frame.Role == "assistant" && texts[frame.MessageID] == nil {
					texts[frame.MessageID] = &strings.Builder{}
					order = append(order, frame.MessageID)
				}
			case "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_CHUNK":
				if frame.MessageID != "" {
					if texts[frame.MessageID] == nil {
						texts[frame.MessageID] = &strings.Builder{}
						order = append(order, frame.MessageID)
					}
					texts[frame.MessageID].WriteString(frame.Delta)
				}
			}
		}
		if onEvent != nil {
			if err = onEvent(event); err != nil {
				return nil, stream.observationError(err)
			}
		}
		if root && (frame.Type == "RUN_FINISHED" || frame.Type == "RUN_ERROR") {
			result.LastEventID = stream.LastEventID()
			result.Outcome = frame.Outcome
			result.Usage = append(json.RawMessage(nil), frame.Usage...)
			parts := make([]string, 0, len(order))
			for _, id := range order {
				parts = append(parts, texts[id].String())
			}
			result.Content = strings.Join(parts, "\n")
			if frame.Type == "RUN_ERROR" {
				return result, &AGUIRunError{RunID: stream.RunID, Code: frame.Code, Message: frame.Message}
			}
			if frame.ThreadID != stream.ThreadID || frame.RunID != stream.RunID {
				return nil, stream.observationError(fmt.Errorf("terminal run identity mismatch"))
			}
			return result, nil
		}
	}
}
func (c *HTTPClient) QueryAGUI(ctx context.Context, input *agentsvc.QueryInput, onEvent func(AGUIEvent) error) (*AGUIRunResult, error) {
	prepared, err := c.PrepareAGUIChat(ctx, input)
	if err != nil {
		return nil, err
	}
	stream, err := c.RunAGUI(ctx, &prepared.Input, nil)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	return CollectAGUI(stream, prepared.ConversationID, onEvent)
}
