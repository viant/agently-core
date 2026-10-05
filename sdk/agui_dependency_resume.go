package sdk

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/runtime/requestctx"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/shared/toolexec"
)

type aguiGraphRuntime interface {
	aguiResumeGraph(context.Context, *agentsvc.QueryInput, string, aguiPending, *agentsvc.QueryOutput) error
}

const aguiMaximumDependencies = 128

func aguiDependencyTurn(conversationID, turnID string) string {
	return conversationID + "\x00" + turnID
}

// aguiResumeGraph resumes existing logical turns from leaf to root. Original
// call and child-turn rows are authoritative checkpoints, so a child already
// completed before a process crash is read rather than executed again.
func (c *backendClient) aguiResumeGraph(ctx context.Context, input *agentsvc.QueryInput, rootTurnID string, pending aguiPending, output *agentsvc.QueryOutput) error {
	if c == nil || c.agent == nil || c.conv == nil || c.data == nil || input == nil || output == nil {
		return fmt.Errorf("dependency continuation requires native runtime and input/output")
	}
	if len(pending.Dependencies) > aguiMaximumDependencies {
		return fmt.Errorf("dependency graph exceeds %d edges", aguiMaximumDependencies)
	}
	byParent := map[string][]clienttool.Dependency{}
	byChild := map[string]clienttool.Dependency{}
	for _, edge := range pending.Dependencies {
		if edge.ID == "" || edge.ParentCall.ID == "" || edge.ParentCall.ToolMessageID == "" || edge.ParentCall.ConversationID == "" || edge.ParentCall.TurnID == "" || edge.ChildConversationID == "" || edge.ChildTurnID == "" || edge.ResultAdapter != clienttool.AgentRunResultV1 {
			return fmt.Errorf("invalid durable dependency edge")
		}
		childKey := aguiDependencyTurn(edge.ChildConversationID, edge.ChildTurnID)
		if _, exists := byChild[childKey]; exists {
			return fmt.Errorf("duplicate dependency child turn")
		}
		byChild[childKey] = edge
		parentKey := aguiDependencyTurn(edge.ParentCall.ConversationID, edge.ParentCall.TurnID)
		byParent[parentKey] = append(byParent[parentKey], edge)
	}
	rootKey := aguiDependencyTurn(input.ConversationID, rootTurnID)
	visiting, visited := map[string]bool{}, map[string]bool{}
	var validate func(string) error
	validate = func(key string) error {
		if visiting[key] {
			return fmt.Errorf("cyclic dependency graph")
		}
		if visited[key] {
			return nil
		}
		visiting[key] = true
		for _, edge := range byParent[key] {
			if err := validate(aguiDependencyTurn(edge.ChildConversationID, edge.ChildTurnID)); err != nil {
				return err
			}
		}
		visiting[key] = false
		visited[key] = true
		return nil
	}
	if err := validate(rootKey); err != nil {
		return err
	}
	if len(visited) != len(byChild)+1 {
		return fmt.Errorf("dependency graph is not connected to the admitted root turn")
	}
	session := clienttool.FromContext(ctx)
	if session == nil {
		return fmt.Errorf("dependency continuation requires restored client tool definitions")
	}
	definitions := session.Definitions()
	paused := false
	var nextCalls []clienttool.PendingCall
	nextEdges := append([]clienttool.Dependency(nil), pending.Dependencies...)
	mergeEdges := func(edges []clienttool.Dependency) {
		for _, fresh := range edges {
			found := false
			for i, old := range nextEdges {
				if old.ID == fresh.ID {
					nextEdges[i] = fresh
					found = true
					break
				}
			}
			if !found {
				nextEdges = append(nextEdges, fresh)
			}
		}
	}
	var resumeChildren func(string) error
	resumeChildren = func(key string) error {
		for _, edge := range byParent[key] {
			status, err := c.aguiDependencyCallStatus(ctx, edge.ParentCall)
			if err != nil {
				return err
			}
			if status == "completed" || status == "failed" || status == "canceled" || status == "cancelled" {
				continue
			}
			if status != "waiting_for_user" {
				return fmt.Errorf("dependency parent call is not waiting: %s", status)
			}
			if err = resumeChildren(aguiDependencyTurn(edge.ChildConversationID, edge.ChildTurnID)); err != nil {
				return err
			}
			if paused {
				return nil
			}
			result, waiting, err := c.aguiDependencyChildResult(ctx, edge)
			if err != nil {
				return err
			}
			if waiting {
				childSession, err := clienttool.NewSession(definitions)
				if err != nil {
					return err
				}
				childCtx := clienttool.WithContinuationDependency(clienttool.WithSession(ctx, childSession), edge)
				invocation := requestctx.Invocation{ID: edge.ID, ConversationID: edge.ChildConversationID, TurnID: edge.ChildTurnID, Name: edge.ChildAgentID, ExecutionMode: edge.ExecutionMode, ParentConversationID: input.ConversationID, ParentTurnID: rootTurnID}
				if edge.ParentCall.ConversationID == input.ConversationID && edge.ParentCall.TurnID == rootTurnID {
					invocation.ParentToolCallID = edge.ParentCall.ID
					invocation.ParentMessageID = edge.ParentCall.AssistantMessageID
				}
				observed, err := requestctx.ObserveInvocation(childCtx, invocation)
				if err != nil {
					return err
				}
				childCtx = observed
				childOutput := &agentsvc.QueryOutput{}
				childInput := &agentsvc.QueryInput{ConversationID: edge.ChildConversationID, UserId: input.UserId}
				runErr := c.agent.ResumeContinuation(childCtx, childInput, edge.ChildTurnID, 0, childOutput)
				returned := requestctx.InvocationResult{Invocation: invocation, NativeStatus: childOutput.ExecutionStatus, Content: childOutput.Content, ClientToolCalls: childOutput.ClientToolCalls, ClientToolDependencies: childOutput.ClientToolDependencies}
				if runErr != nil {
					returned.Error = runErr.Error()
				}
				if err = requestctx.NotifyInvocationReturned(childCtx, returned); err != nil {
					return err
				}
				if runErr != nil {
					return runErr
				}
				if childOutput.ExecutionStatus == "waiting_for_user" {
					paused = true
					nextCalls = append(nextCalls, childOutput.ClientToolCalls...)
					mergeEdges(childOutput.ClientToolDependencies)
					return nil
				}
				result = clienttool.ChildResult{Content: childOutput.Content, Status: childOutput.ExecutionStatus, ConversationID: edge.ChildConversationID, TurnID: edge.ChildTurnID}
			}
			if err = toolexec.CompleteDependency(ctx, c.conv, edge, result); err != nil {
				return err
			}
		}
		return nil
	}
	if err := resumeChildren(rootKey); err != nil {
		return err
	}
	if paused {
		output.ConversationID = input.ConversationID
		output.TurnID = rootTurnID
		output.MessageID = rootTurnID
		output.ExecutionStatus = "waiting_for_user"
		for i := range nextCalls {
			nextCalls[i].ProtocolID = agui.ProtocolToolCallID(nextCalls[i].TurnID, nextCalls[i].ID)
		}
		output.ClientToolCalls = nextCalls
		for _, edge := range nextEdges {
			status, err := c.aguiDependencyCallStatus(ctx, edge.ParentCall)
			if err != nil {
				return err
			}
			if status == "waiting_for_user" {
				output.ClientToolDependencies = append(output.ClientToolDependencies, edge)
			}
		}
		return nil
	}
	rootConv, err := c.conv.GetConversation(ctx, input.ConversationID, conversation.WithIncludeTranscript(true), conversation.WithIncludeModelCall(true))
	if err != nil {
		return err
	}
	if rootConv == nil {
		return fmt.Errorf("dependency root conversation disappeared")
	}
	for _, turn := range rootConv.GetTranscript() {
		if turn != nil && turn.Id == rootTurnID {
			switch turn.Status {
			case "succeeded", "completed", "failed", "canceled", "cancelled":
				result := aguiDependencyFinalResult(turn, input.ConversationID)
				output.ConversationID = input.ConversationID
				output.TurnID = rootTurnID
				output.MessageID = rootTurnID
				output.ExecutionStatus = result.Status
				output.Content = result.Content
				if result.Status == "failed" {
					message := result.Error
					if message == "" {
						message = "native root execution failed"
					}
					return fmt.Errorf("%s", message)
				}
				return nil
			}
		}
	}
	rootSession, err := clienttool.NewSession(definitions)
	if err != nil {
		return err
	}
	return c.agent.ResumeContinuation(clienttool.WithSession(ctx, rootSession), input, rootTurnID, 0, output)
}
func (c *backendClient) aguiDependencyCallStatus(ctx context.Context, call clienttool.PendingCall) (string, error) {
	message, err := c.conv.GetMessage(ctx, call.ToolMessageID, conversation.WithIncludeToolCall(true))
	if err != nil {
		return "", err
	}
	if message == nil || message.Role != "tool" || message.ConversationId != call.ConversationID || message.TurnId == nil || *message.TurnId != call.TurnID || message.MessageToolCall == nil || message.MessageToolCall.OpId != call.ID {
		return "", fmt.Errorf("dependency original call identity mismatch")
	}
	return message.MessageToolCall.Status, nil
}
func (c *backendClient) aguiDependencyChildResult(ctx context.Context, edge clienttool.Dependency) (clienttool.ChildResult, bool, error) {
	for {
		conv, err := c.conv.GetConversation(ctx, edge.ChildConversationID, conversation.WithIncludeTranscript(true), conversation.WithIncludeModelCall(true), conversation.WithIncludeToolCall(true))
		if err != nil {
			return clienttool.ChildResult{}, false, err
		}
		if conv == nil || conv.ConversationParentId == nil || *conv.ConversationParentId != edge.ParentCall.ConversationID || conv.ConversationParentTurnId == nil || *conv.ConversationParentTurnId != edge.ParentCall.TurnID {
			return clienttool.ChildResult{}, false, fmt.Errorf("dependency native child ancestry mismatch")
		}
		found := false
		for _, turn := range conv.GetTranscript() {
			if turn == nil || turn.Id != edge.ChildTurnID {
				continue
			}
			found = true
			result := clienttool.ChildResult{ConversationID: edge.ChildConversationID, TurnID: edge.ChildTurnID, Status: turn.Status, Error: valueOrEmpty(turn.ErrorMessage)}
			switch strings.ToLower(turn.Status) {
			case "waiting_for_user", "blocked":
				return result, true, nil
			case "succeeded", "completed", "failed", "canceled", "cancelled":
				result = aguiDependencyFinalResult(turn, edge.ChildConversationID)
				return result, false, nil
			case "running", "thinking", "queued", "pending":
			default:
				return result, false, fmt.Errorf("dependency child has unknown native status %q", turn.Status)
			}
			break
		}
		if !found {
			return clienttool.ChildResult{}, false, fmt.Errorf("dependency native child turn disappeared")
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return clienttool.ChildResult{}, false, ctx.Err()
		case <-timer.C:
		}
	}
}

func aguiDependencyFinalResult(turn *conversation.Turn, conversationID string) clienttool.ChildResult {
	result := clienttool.ChildResult{ConversationID: conversationID, TurnID: turn.Id, Status: turn.Status, Error: valueOrEmpty(turn.ErrorMessage)}
	var latest *conversation.Message
	for _, raw := range turn.Message {
		if raw == nil || raw.Role != "assistant" || raw.Type != "text" || raw.Interim != 0 || (raw.Archived != nil && *raw.Archived == 1) {
			continue
		}
		message := (*conversation.Message)(raw)
		if latest == nil || message.CreatedAt.After(latest.CreatedAt) {
			latest = message
		}
	}
	if latest != nil {
		result.Content = latest.GetContentPreferContent()
	}
	return result
}
