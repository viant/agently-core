package planner

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Application struct {
	ToolBundles       []string
	TemplateID        string
	ParallelToolCalls *bool
	Context           map[string]any
}

type GuidanceDoc struct {
	ID      string
	Summary string
	Content string
}

type GuidanceMeta struct {
	Status          string
	StaticProfile   string
	SecondPolicy    string
	OutputPayloadID string
	Validated       *bool
}

func ApplyOutput(app *Application, out Output, pctx *PlannerContext) {
	if app == nil || len(out) == 0 {
		return
	}
	if values := OutputStringSlice(out, "toolBundles"); len(values) > 0 {
		app.ToolBundles = append(app.ToolBundles, values...)
	}
	if value := OutputString(out, "templateId"); value != "" && strings.TrimSpace(app.TemplateID) == "" {
		app.TemplateID = value
	}
	if ptr := OutputBoolPtr(out, "parallelToolCalls"); ptr != nil && app.ParallelToolCalls == nil {
		v := *ptr
		app.ParallelToolCalls = &v
	}
	if pctx != nil {
		if app.Context == nil {
			app.Context = make(map[string]any)
		}
		app.Context[ContextKey] = pctx
	}
}

func BuildGuidanceDocs(turnID string, out Output, pctx *PlannerContext, meta GuidanceMeta, errs []ValidationError) []GuidanceDoc {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil
	}
	docs := []GuidanceDoc{
		{
			ID:      "planner-strategy:" + turnID,
			Summary: "planner://strategy",
			Content: renderPlannerStrategy(out, pctx, meta),
		},
		{
			ID:      "planner-evidence:" + turnID,
			Summary: "planner://evidence",
			Content: renderPlannerEvidence(out),
		},
		{
			ID:      "planner-guards:" + turnID,
			Summary: "planner://guards",
			Content: renderPlannerGuards(out),
		},
		{
			ID:      "planner-policy:" + turnID,
			Summary: "planner://policy",
			Content: renderPlannerPolicy(out, meta, errs),
		},
	}
	filtered := make([]GuidanceDoc, 0, len(docs))
	for _, doc := range docs {
		if strings.TrimSpace(doc.Content) == "" {
			continue
		}
		filtered = append(filtered, doc)
	}
	return filtered
}

func renderPlannerStrategy(out Output, pctx *PlannerContext, meta GuidanceMeta) string {
	var parts []string
	if value := strings.TrimSpace(meta.Status); value != "" {
		parts = append(parts, "Status: "+value)
	}
	if pctx != nil {
		if value := strings.TrimSpace(string(pctx.Trigger)); value != "" {
			parts = append(parts, "Trigger: "+value)
		}
		if pctx.Attempt > 0 {
			parts = append(parts, fmt.Sprintf("Attempt: %d", pctx.Attempt))
		}
	}
	if value := strings.TrimSpace(meta.StaticProfile); value != "" {
		parts = append(parts, "StaticProfile: "+value)
	}
	if meta.Validated != nil {
		parts = append(parts, fmt.Sprintf("Validated: %t", *meta.Validated))
	}
	if value := strings.TrimSpace(meta.OutputPayloadID); value != "" {
		parts = append(parts, "OutputPayloadID: "+value)
	}
	if len(out) == 0 {
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	if value := OutputString(out, "strategyFamily"); value != "" {
		parts = append(parts, "StrategyFamily: "+value)
	}
	if values := OutputStringSlice(out, "baseProfiles"); len(values) > 0 {
		parts = append(parts, "BaseProfiles: "+strings.Join(values, ", "))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func renderPlannerEvidence(out Output) string {
	if len(out) == 0 {
		return ""
	}
	var parts []string
	if values := OutputStringSlice(out, "requiredEvidence"); len(values) > 0 {
		parts = append(parts, "RequiredEvidence: "+strings.Join(values, ", "))
	}
	if values := OutputStringSlice(out, "executionOrder"); len(values) > 0 {
		parts = append(parts, "ExecutionOrder: "+strings.Join(values, ", "))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func renderPlannerGuards(out Output) string {
	if len(out) == 0 {
		return ""
	}
	values := OutputStringSlice(out, "finalizationGuards")
	if len(values) == 0 {
		return ""
	}
	return "FinalizationGuards: " + strings.Join(values, ", ")
}

func renderPlannerPolicy(out Output, meta GuidanceMeta, errs []ValidationError) string {
	var parts []string
	if value := strings.TrimSpace(meta.SecondPolicy); value != "" {
		parts = append(parts, "SecondPolicy: "+value)
	}
	if meta.Validated != nil {
		parts = append(parts, fmt.Sprintf("Validated: %t", *meta.Validated))
	}
	if len(out) != 0 {
		if values := OutputMap(out, "narrationPolicy"); len(values) > 0 {
			if raw, err := json.MarshalIndent(values, "", "  "); err == nil {
				parts = append(parts, "NarrationPolicy:\n```json\n"+string(raw)+"\n```")
			}
		}
		if values := OutputMap(out, "workspaceExtensions"); len(values) > 0 {
			if raw, err := json.MarshalIndent(values, "", "  "); err == nil {
				parts = append(parts, "WorkspaceExtensions:\n```json\n"+string(raw)+"\n```")
			}
		}
	}
	if text := FormatValidationErrors(errs); text != "" {
		parts = append(parts, "ValidationErrors:\n"+text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}
