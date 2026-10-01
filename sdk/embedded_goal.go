package sdk

import goalsys "github.com/viant/agently-core/service/goal"

func mapGoalView(view *goalsys.Record) *Goal {
	if view == nil {
		return nil
	}
	out := &Goal{
		ID:              view.ID,
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
