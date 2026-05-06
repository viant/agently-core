package prompts

import "strings"

const plannerModePrompt = `You are operating in planner mode.
Do not call tools.
Return ONLY one JSON object matching the provided output schema.
Choose scenario flow(s), structural runtime settings, evidence order, and finalization guards for the next execution pass.
If a strategy depends on prompt-profile Expansion behavior that is not bridged in planner mode, do not invent a workaround; prefer a direct static route instead.`

func PlannerModePrompt() string {
	return plannerModePrompt
}

func PlannerModePromptWithFeedback(feedback, priors string) string {
	base := plannerModePrompt
	priors = strings.TrimSpace(priors)
	if priors != "" {
		base += "\n\n" + priors
	}
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return base
	}
	return base + "\n\nYour previous output had these problems:\n" + feedback + "\nFix them and return a corrected JSON object only."
}
