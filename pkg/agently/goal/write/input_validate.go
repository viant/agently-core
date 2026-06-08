package write

import (
	"context"
	"fmt"
	"strings"

	"github.com/viant/xdatly/handler"
	"github.com/viant/xdatly/handler/validator"
)

func (i *Input) Validate(ctx context.Context, sess handler.Session, output *Output) error {
	_ = ctx
	_ = sess
	for _, goal := range i.Goals {
		if goal == nil {
			continue
		}
		if strings.TrimSpace(goal.Id) == "" {
			output.Violations = append(output.Violations, &validator.Violation{
				Location: "Goals.Id",
				Message:  "id is required",
			})
			continue
		}
		if _, ok := i.CurGoalById[goal.Id]; !ok {
			if goal.ConversationID == nil || strings.TrimSpace(*goal.ConversationID) == "" {
				output.Violations = append(output.Violations, &validator.Violation{
					Location: "Goals.ConversationID",
					Message:  "conversation_id is required for insert",
				})
			}
			if goal.Objective == nil || strings.TrimSpace(*goal.Objective) == "" {
				output.Violations = append(output.Violations, &validator.Violation{
					Location: "Goals.Objective",
					Message:  "objective is required for insert",
				})
			}
			if goal.Status == nil || strings.TrimSpace(*goal.Status) == "" {
				output.Violations = append(output.Violations, &validator.Violation{
					Location: "Goals.Status",
					Message:  "status is required for insert",
				})
			}
		}
	}
	if len(output.Violations) > 0 {
		return fmt.Errorf("failed validation")
	}
	return nil
}

func (i *Input) canUseMarkerProvider(v interface{}) bool {
	switch actual := v.(type) {
	case *Goal:
		_, ok := i.CurGoalById[actual.Id]
		return ok
	default:
		return true
	}
}
