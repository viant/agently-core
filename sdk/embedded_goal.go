package sdk

import aggoal "github.com/viant/agently-core/pkg/agently/goal"

func mapGoalView(view *aggoal.GoalView) *Goal {
	if view == nil {
		return nil
	}
	out := &Goal{
		ID:              view.Id,
		ConversationID:  view.ConversationID,
		Objective:       view.Objective,
		Status:          view.Status,
		TokenBudget:     view.TokenBudget,
		TokensUsed:      view.TokensUsed,
		TimeUsedSeconds: view.TimeUsedSeconds,
	}
	if view.StatusReason != nil {
		out.StatusReason = *view.StatusReason
	}
	if view.PauseReason != nil {
		out.PauseReason = *view.PauseReason
	}
	if view.ControllerSpec != nil {
		out.ControllerSpec = *view.ControllerSpec
	}
	return out
}
