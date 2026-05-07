package agent

import (
	"strings"

	"github.com/viant/agently-core/internal/textutil"
	planner "github.com/viant/agently-core/service/planner"

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

type PlannerPassTrace struct {
	ConversationID  string
	TurnID          string
	Attempt         int
	Validated       bool
	StrategyFamily  string
	BaseProfiles    []string
	ToolBundles     []string
	TemplateID      string
	EvidenceCount   int
	ExecutionOrder  []string
	Guards          []string
	ValidatorErrors []planner.ValidationError
}

func (p *PlannerPassTrace) AsMap() map[string]any {
	if p == nil {
		return nil
	}
	return map[string]any{
		"conversationID":  strings.TrimSpace(p.ConversationID),
		"turnID":          strings.TrimSpace(p.TurnID),
		"attempt":         p.Attempt,
		"validated":       p.Validated,
		"strategyFamily":  strings.TrimSpace(p.StrategyFamily),
		"baseProfiles":    append([]string(nil), p.BaseProfiles...),
		"toolBundles":     append([]string(nil), p.ToolBundles...),
		"templateId":      strings.TrimSpace(p.TemplateID),
		"evidenceCount":   p.EvidenceCount,
		"executionOrder":  append([]string(nil), p.ExecutionOrder...),
		"guards":          append([]string(nil), p.Guards...),
		"validatorErrors": append([]planner.ValidationError(nil), p.ValidatorErrors...),
	}
}

func plannerContextTrace(pc *planner.PlannerContext) map[string]any {
	if pc == nil {
		return nil
	}
	data := planner.Output(pc.Data)
	return map[string]any{
		"trigger":         strings.TrimSpace(string(pc.Trigger)),
		"attempt":         pc.Attempt,
		"strategyFamily":  planner.OutputString(data, "strategyFamily"),
		"baseProfiles":    planner.OutputStringSlice(data, "baseProfiles"),
		"toolBundles":     planner.OutputStringSlice(data, "toolBundles"),
		"templateId":      planner.OutputString(data, "templateId"),
		"executionOrder":  planner.OutputStringSlice(data, "executionOrder"),
		"guards":          planner.OutputStringSlice(data, "finalizationGuards"),
		"evidenceCount":   len(planner.OutputStringSlice(data, "requiredEvidence")),
		"narrationPolicy": planner.OutputMap(data, "narrationPolicy"),
	}
}
