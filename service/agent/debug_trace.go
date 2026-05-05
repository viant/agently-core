package agent

import (
	"strings"

	"github.com/viant/agently-core/internal/textutil"

	"github.com/viant/agently-core/protocol/agent/execution"
)

func summarizePlanSteps(aPlan *execution.Plan) []map[string]any {
	if aPlan == nil || len(aPlan.Steps) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(aPlan.Steps))
	for _, step := range aPlan.Steps {
		result = append(result, map[string]any{
			"id":         strings.TrimSpace(step.ID),
			"type":       strings.TrimSpace(step.Type),
			"name":       strings.TrimSpace(step.Name),
			"args":       step.Args,
			"reasonHead": textutil.Head(strings.TrimSpace(step.Reason), 200),
		})
	}
	return result
}
