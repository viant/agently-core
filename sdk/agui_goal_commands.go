package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/viant/agently-core/protocol/agui/extensions"
	systemgoal "github.com/viant/agently-core/protocol/tool/service/system/goal"
	"github.com/viant/agently-core/runtime/requestctx"
	goalsys "github.com/viant/agently-core/service/goal"
)

// AGUIGoalLifecycleClient preserves the canonical pause/resume hooks without
// widening the native Client interface or forcing resource commands through Query.
type AGUIGoalLifecycleClient interface {
	PauseGoal(ctx context.Context, threadID, reason string) (*Goal, error)
	ResumeGoal(ctx context.Context, threadID string) (*Goal, error)
}

// AGUIGoalSnapshot includes known zero counters and known absent budgets/schedules
// explicitly. It projects the same user-facing fields as the current SDK Goal.
type AGUIGoalSnapshot struct {
	ID                 string                  `json:"id"`
	ConversationID     string                  `json:"conversationId"`
	Objective          string                  `json:"objective"`
	Status             string                  `json:"status"`
	StatusReason       string                  `json:"statusReason"`
	PauseReason        string                  `json:"pauseReason"`
	ControllerSpec     string                  `json:"controllerSpec"`
	ControllerSchedule *GoalControllerSchedule `json:"controllerSchedule"`
	TokenBudget        *int64                  `json:"tokenBudget"`
	TokensUsed         int64                   `json:"tokensUsed"`
	TimeUsedSeconds    int64                   `json:"timeUsedSeconds"`
}

type AGUIGoalCommandResult struct {
	Version string            `json:"version"`
	Goal    *AGUIGoalSnapshot `json:"goal"`
	Cleared bool              `json:"cleared,omitempty"`
}

func goalCommandResult(goal *Goal, cleared bool) *AGUIGoalCommandResult {
	result := &AGUIGoalCommandResult{Version: "1", Cleared: cleared}
	if goal != nil {
		result.Goal = &AGUIGoalSnapshot{ID: goal.ID, ConversationID: goal.ConversationID, Objective: goal.Objective, Status: goal.Status, StatusReason: goal.StatusReason, PauseReason: goal.PauseReason, ControllerSpec: goal.ControllerSpec, ControllerSchedule: goal.ControllerSchedule, TokenBudget: goal.TokenBudget, TokensUsed: goal.TokensUsed, TimeUsedSeconds: goal.TimeUsedSeconds}
	}
	return result
}

// dispatchAGUIGoal executes deterministic resource commands. The enclosing run
// owns authenticated scope, command admission, journaling and standard boundaries.
func dispatchAGUIGoal(ctx context.Context, client Client, threadID, operation string, payload json.RawMessage) (result any, handled bool, err error) {
	switch operation {
	case "goal.get", "goal.create", "goal.update", "goal.clear", "goal.pause", "goal.resume":
	default:
		return nil, false, nil
	}
	handled = true
	if client == nil {
		return nil, true, fmt.Errorf("goal client is required")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, true, fmt.Errorf("goal threadId is required")
	}
	if err = extensions.ValidateGoalPayload(operation, payload); err != nil {
		return nil, true, err
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		payload = json.RawMessage(`{}`)
	}
	var goal *Goal
	switch operation {
	case "goal.get":
		goal, err = client.GetGoal(ctx, threadID)
	case "goal.clear":
		if err = client.ClearGoal(ctx, threadID); err == nil {
			return goalCommandResult(nil, true), true, nil
		}
	case "goal.create":
		var input struct {
			Objective      string          `json:"objective"`
			TokenBudget    *int64          `json:"tokenBudget,omitempty"`
			ControllerSpec json.RawMessage `json:"controllerSpec,omitempty"`
		}
		if err = json.Unmarshal(payload, &input); err != nil {
			return nil, true, err
		}
		if strings.TrimSpace(input.Objective) == "" {
			return nil, true, fmt.Errorf("goal objective is required")
		}
		var spec string
		if spec, err = canonicalGoalControllerSpec(input.ControllerSpec); err != nil {
			return nil, true, err
		}
		goal, err = client.CreateGoal(ctx, &CreateGoalInput{ConversationID: threadID, Objective: input.Objective, TokenBudget: input.TokenBudget, ControllerSpec: spec})
	case "goal.update":
		var input UpdateGoalInput
		if err = json.Unmarshal(payload, &input); err != nil {
			return nil, true, err
		}
		if input.Objective != "" && strings.TrimSpace(input.Objective) == "" || input.StatusReason != "" && strings.TrimSpace(input.StatusReason) == "" {
			return nil, true, fmt.Errorf("goal update text cannot be blank")
		}
		input.ConversationID = threadID
		goal, err = client.UpdateGoal(ctx, &input)
	case "goal.pause":
		lifecycle, ok := client.(AGUIGoalLifecycleClient)
		if !ok {
			return nil, true, fmt.Errorf("goal pause lifecycle is unsupported by this backend")
		}
		var input struct {
			Reason string `json:"reason,omitempty"`
		}
		if err = json.Unmarshal(payload, &input); err != nil {
			return nil, true, err
		}
		reason := strings.TrimSpace(input.Reason)
		if reason == "" {
			reason = string(goalsys.PauseReasonUserRequested)
		}
		goal, err = lifecycle.PauseGoal(ctx, threadID, reason)
	case "goal.resume":
		lifecycle, ok := client.(AGUIGoalLifecycleClient)
		if !ok {
			return nil, true, fmt.Errorf("goal resume lifecycle is unsupported by this backend")
		}
		goal, err = lifecycle.ResumeGoal(ctx, threadID)
	}
	if err != nil {
		return nil, true, err
	}
	return goalCommandResult(goal, false), true, nil
}

func canonicalGoalControllerSpec(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "", nil
		}
		raw = json.RawMessage(text)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var spec goalsys.ControllerSpec
	if err := decoder.Decode(&spec); err != nil {
		return "", fmt.Errorf("invalid goal controllerSpec: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", fmt.Errorf("invalid trailing goal controllerSpec")
	}
	return spec.Encode()
}

func (c *backendClient) PauseGoal(ctx context.Context, threadID, reason string) (*Goal, error) {
	return c.transitionAGUIGoal(ctx, threadID, "pause", reason)
}
func (c *backendClient) ResumeGoal(ctx context.Context, threadID string) (*Goal, error) {
	return c.transitionAGUIGoal(ctx, threadID, "resume", "")
}
func (c *backendClient) transitionAGUIGoal(ctx context.Context, threadID, method, reason string) (*Goal, error) {
	// GetGoal enforces workspace enablement and existing conversation visibility
	// before the model-tool service receives a conversation-scoped context.
	current, err := c.GetGoal(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, fmt.Errorf("goal does not exist for current conversation")
	}
	service := systemgoal.New(c.goalRepo)
	if c.schedulerSvc != nil {
		service.SetWakeupCanceler(c.schedulerSvc)
		service.SetControllerScheduleReader(c.schedulerSvc)
	}
	ctx = requestctx.WithConversationID(ctx, threadID)
	// A caller's previous turn context must not override the command's target.
	ctx = requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: threadID})
	execute, err := service.Method(method)
	if err != nil {
		return nil, err
	}
	switch method {
	case "pause":
		err = execute(ctx, &systemgoal.PauseInput{Reason: reason}, &systemgoal.PauseOutput{})
	case "resume":
		err = execute(ctx, &systemgoal.ResumeInput{}, &systemgoal.ResumeOutput{})
	default:
		return nil, fmt.Errorf("unsupported goal lifecycle %q", method)
	}
	if err != nil {
		return nil, err
	}
	return c.GetGoal(ctx, threadID)
}
