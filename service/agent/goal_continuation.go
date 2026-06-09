package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/viant/agently-core/protocol/agent/execution"
	goalruntime "github.com/viant/agently-core/service/goal"
)

var continuationHintLineRE = regexp.MustCompile(`(?m)^continuationHint:\s*(.+?)\s*$`)

func buildGoalContinuationHint(output *QueryOutput) *goalruntime.ContinuationHint {
	if output == nil {
		return nil
	}
	if hint := continuationHintFromContent(output.Content); hint != nil {
		return hint
	}
	if hint := continuationHintFromPlan(output.Plan); hint != nil {
		return hint
	}
	return nil
}

func buildGoalProgressFingerprint(output *QueryOutput, continuation *goalruntime.ContinuationHint) string {
	parts := []string{}
	if output != nil {
		if content := strings.TrimSpace(output.Content); content != "" {
			parts = append(parts, "content:"+content)
		}
		if plan := summarizeProgressPlan(output.Plan); plan != "" {
			parts = append(parts, "plan:"+plan)
		}
	}
	if continuation != nil {
		parts = append(parts,
			"continuation_reason:"+strings.TrimSpace(continuation.Reason),
			"continuation_preview:"+strings.TrimSpace(continuation.Preview),
			"continuation_payload:"+strings.TrimSpace(continuation.Payload),
		)
	}
	if len(parts) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func continuationHintFromContent(content string) *goalruntime.ContinuationHint {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "{") {
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &payload); err == nil {
			hint := strings.TrimSpace(continuationStringValue(payload["continuationHint"]))
			if hint != "" {
				return buildExplicitContinuationHint(hint, payload["nextArgs"])
			}
		}
	}
	if matches := continuationHintLineRE.FindStringSubmatch(trimmed); len(matches) == 2 {
		return buildExplicitContinuationHint(strings.TrimSpace(matches[1]), nil)
	}
	return nil
}

func buildExplicitContinuationHint(hint string, nextArgs interface{}) *goalruntime.ContinuationHint {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return nil
	}
	payloadLines := []string{
		"Continue working toward the active goal.",
		"Follow the explicit continuation guidance returned by the last turn:",
		hint,
	}
	if nextArgs != nil {
		if encoded, err := json.Marshal(nextArgs); err == nil && len(encoded) > 2 {
			payloadLines = append(payloadLines, "Suggested next arguments: "+string(encoded))
		}
	}
	return &goalruntime.ContinuationHint{
		Reason:  "continue explicit tool-result continuation",
		Preview: "Continue: " + textHead(hint, 140),
		Payload: strings.Join(payloadLines, "\n"),
	}
}

func continuationHintFromPlan(plan *execution.Plan) *goalruntime.ContinuationHint {
	if plan == nil {
		return nil
	}
	for _, step := range plan.Steps {
		summary := summarizeContinuationStep(step)
		if summary == "" {
			continue
		}
		payloadLines := []string{
			"Continue working toward the active goal.",
			"Next planned step: " + summary,
		}
		if reason := strings.TrimSpace(step.Reason); reason != "" && !strings.EqualFold(reason, summary) {
			payloadLines = append(payloadLines, "Step reason: "+reason)
		}
		if len(step.Args) > 0 {
			if encoded, err := json.Marshal(step.Args); err == nil && len(encoded) > 2 {
				payloadLines = append(payloadLines, "Step arguments: "+string(encoded))
			}
		}
		reason := "continue planned step"
		if name := strings.TrimSpace(step.Name); name != "" {
			reason = fmt.Sprintf("continue planned step: %s", textHead(name, 80))
		}
		return &goalruntime.ContinuationHint{
			Reason:  reason,
			Preview: "Continue: " + textHead(summary, 140),
			Payload: strings.Join(payloadLines, "\n"),
		}
	}
	if intention := strings.TrimSpace(plan.Intention); intention != "" {
		return &goalruntime.ContinuationHint{
			Reason:  "continue planned intention",
			Preview: "Continue: " + textHead(intention, 140),
			Payload: "Continue working toward the active goal.\nPlanned intention: " + intention,
		}
	}
	return nil
}

func summarizeContinuationStep(step execution.Step) string {
	name := strings.TrimSpace(step.Name)
	content := strings.TrimSpace(step.Content)
	reason := strings.TrimSpace(step.Reason)
	stepType := strings.TrimSpace(step.Type)

	switch {
	case stepType == "tool" && name != "":
		return "Run tool " + name
	case content != "":
		return content
	case reason != "":
		return reason
	case name != "":
		return name
	case stepType != "":
		return stepType
	default:
		return ""
	}
}

func summarizeProgressPlan(plan *execution.Plan) string {
	if plan == nil {
		return ""
	}
	parts := []string{}
	if intention := strings.TrimSpace(plan.Intention); intention != "" {
		parts = append(parts, "intention:"+intention)
	}
	for _, step := range plan.Steps {
		summary := summarizeContinuationStep(step)
		if summary == "" {
			continue
		}
		part := fmt.Sprintf("step:%s|type:%s|name:%s|reason:%s", summary, strings.TrimSpace(step.Type), strings.TrimSpace(step.Name), strings.TrimSpace(step.Reason))
		if len(step.Args) > 0 {
			if encoded, err := json.Marshal(step.Args); err == nil {
				part += "|args:" + string(encoded)
			}
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "\n")
}

func textHead(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return strings.TrimSpace(value[:max-3]) + "..."
}

func continuationStringValue(value interface{}) string {
	switch actual := value.(type) {
	case string:
		return actual
	default:
		return ""
	}
}
