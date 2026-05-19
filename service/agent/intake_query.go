package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/internal/logx"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	intakesvc "github.com/viant/agently-core/service/intake"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
)

// maybeRunIntakeSidecar runs the pre-turn intake sidecar when the agent is
// configured for it.  It is a no-op when:
//   - the intake service is not wired
//   - the agent's Intake.Enabled is false
//   - this is not the first turn and TriggerOnTopicShift is false
//   - the caller already provided an intake Context via RunInput.WorkspaceIntake
//     (skip rule §2.c — caller's value short-circuits the LLM call but the
//     merge logic still runs through applyTurnContext)
//
// On success the intake Context is stored in input.Context so the agent can read
// title, intent, context, and (when in Class B scope) profile suggestions.
// AppendToolBundles are merged into input.ToolBundles.
// A high-confidence SuggestedProfileId is stored as a hint under a well-known
// context key — the orchestrator may use it or override it.
func (s *Service) maybeRunIntakeSidecar(ctx context.Context, input *QueryInput) {
	if s == nil || input == nil || input.Agent == nil {
		return
	}
	cfg := &input.Agent.Intake
	runCfg := *cfg
	if bootstrap := s.workspaceUIBootstrap(ctx, input.ConversationID); strings.TrimSpace(bootstrap) != "" {
		if extra := strings.TrimSpace(runCfg.Prompt.Text); extra != "" {
			runCfg.Prompt.Text = extra + "\n\nWorkspace UI bootstrap:\n" + bootstrap
		} else {
			runCfg.Prompt.Text = "Workspace UI bootstrap:\n" + bootstrap
		}
	}

	if s.maybeInjectWorkspaceUIOverride(ctx, input, &runCfg) {
		return
	}

	// Skip rule §2.c — caller-provided override.
	// run_support.go places RunInput.WorkspaceIntake under intakesvc.ContextKey
	// with Source = SourceCallerProvided. When present, skip the LLM call but
	// still run the merge logic so AppendToolBundles, profile/template
	// suggestions, etc. take effect uniformly across all skip paths.
	if existing := intakesvc.FromContext(input.Context); existing != nil && existing.Routing.Source == intakesvc.SourceCallerProvided {
		logx.Infof("conversation", "intake.skipped.caller-provided convo=%q agent=%q selectedAgent=%q",
			strings.TrimSpace(input.ConversationID),
			strings.TrimSpace(input.Agent.ID),
			strings.TrimSpace(existing.Routing.SelectedAgentID),
		)
		applyTurnContext(input, existing, cfg)
		s.maybeSetConversationTitle(ctx, input.ConversationID, existing.Classification.Title)
		return
	}

	if s.intakeSvc == nil {
		return
	}
	if !runCfg.Enabled {
		return
	}
	logx.Infof("conversation", "intake.consider convo=%q agent=%q promptProfileId=%q triggerOnTopicShift=%v",
		strings.TrimSpace(input.ConversationID),
		strings.TrimSpace(input.Agent.ID),
		strings.TrimSpace(input.PromptProfileId),
		runCfg.TriggerOnTopicShift,
	)
	if !s.shouldRunIntake(ctx, input, &runCfg) {
		logx.Infof("conversation", "intake.skipped.gate convo=%q agent=%q promptProfileId=%q",
			strings.TrimSpace(input.ConversationID),
			strings.TrimSpace(input.Agent.ID),
			strings.TrimSpace(input.PromptProfileId),
		)
		return
	}
	userMessage := strings.TrimSpace(input.Query)
	if userMessage == "" {
		return
	}
	runCtx := s.intakeTrackedContext(ctx, input)
	tc := s.intakeSvc.Run(runCtx, userMessage, &runCfg, strings.TrimSpace(input.UserId))
	if tc == nil {
		return
	}
	s.normalizeIntakeTurnContext(ctx, input, tc, &runCfg)
	logx.Infof("conversation", "intake.done convo=%q agent=%q title=%q intent=%q confidence=%.2f profile=%q",
		strings.TrimSpace(input.ConversationID),
		strings.TrimSpace(input.Agent.ID),
		strings.TrimSpace(tc.Classification.Title),
		strings.TrimSpace(tc.Classification.Intent),
		tc.Classification.Confidence,
		strings.TrimSpace(tc.Prompting.SuggestedProfileID),
	)
	if strings.TrimSpace(tc.DirectAction.ToolName) != "" {
		logx.Infof("conversation", "intake.direct_action convo=%q agent=%q tool=%q assistant_text_len=%d",
			strings.TrimSpace(input.ConversationID),
			strings.TrimSpace(input.Agent.ID),
			strings.TrimSpace(tc.DirectAction.ToolName),
			len(strings.TrimSpace(tc.DirectAction.AssistantText)),
		)
	}
	applyTurnContext(input, tc, &runCfg)
	s.maybeSetConversationTitle(ctx, input.ConversationID, tc.Classification.Title)
}

func (s *Service) maybeInjectWorkspaceUIOverride(ctx context.Context, input *QueryInput, cfg *agentmdl.Intake) bool {
	if s == nil || input == nil {
		return false
	}
	override := s.resolveWorkspaceUIIntentOverride(ctx, input)
	if override == nil {
		return false
	}
	input.Context, _ = intakesvc.StoreCallerProvided(input.Context, override)
	if cfg != nil {
		applyTurnContext(input, override, cfg)
		s.maybeSetConversationTitle(ctx, input.ConversationID, override.Classification.Title)
	}
	logx.Infof("conversation", "intake.workspace_override convo=%q query=%q profile=%q tool=%q", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.Query), strings.TrimSpace(override.Prompting.SuggestedProfileID), strings.TrimSpace(override.DirectAction.ToolName))
	return true
}

func (s *Service) resolveWorkspaceUIIntentOverride(ctx context.Context, input *QueryInput) *intakesvc.Context {
	if input == nil {
		return nil
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	query := strings.TrimSpace(input.Query)
	conversationID = strings.TrimSpace(conversationID)
	if query == "" {
		return nil
	}
	if input.Agent != nil {
		if direct := s.resolveWorkspaceFollowUpRuleOverride(ctx, conversationID, query, input.Agent.Intake.ActivationRules); direct != nil {
			return direct
		}
	}
	return s.resolveWorkspaceActivationProfileOverride(input)
}

func (s *Service) resolveWorkspaceActivationProfileOverride(input *QueryInput) *intakesvc.Context {
	if input == nil || input.Agent == nil {
		return nil
	}
	rules := input.Agent.Intake.ActivationRules
	if len(rules) == 0 {
		return nil
	}
	return resolveActivationRuleOverride(strings.TrimSpace(input.Query), rules)
}

func mustAtoi(value string) int {
	result := 0
	for _, r := range strings.TrimSpace(value) {
		if r < '0' || r > '9' {
			return result
		}
		result = result*10 + int(r-'0')
	}
	return result
}

func resolveActivationRuleOverride(query string, rules []agentmdl.ActivationRule) *intakesvc.Context {
	query = strings.TrimSpace(query)
	if query == "" || len(rules) == 0 {
		return nil
	}
	for _, rule := range rules {
		if override := evaluateActivationRule(query, rule); override != nil {
			return override
		}
	}
	return nil
}

func evaluateActivationRule(query string, rule agentmdl.ActivationRule) *intakesvc.Context {
	patterns := activationRulePatterns(rule.Match)
	if len(patterns) == 0 {
		return nil
	}
	for _, pattern := range patterns {
		re, err := compileActivationRegex(pattern, rule.Match.Flags)
		if err != nil {
			continue
		}
		matches := re.FindStringSubmatch(query)
		if len(matches) == 0 {
			continue
		}
		vars, ok := buildActivationVariables(query, matches, rule.Match.Extractors)
		if !ok {
			continue
		}
		override := &intakesvc.Context{
			Classification: intakesvc.ClassificationContext{
				Title:      query,
				Intent:     firstNonEmpty(strings.TrimSpace(rule.Classification.Intent), "summary"),
				Confidence: activationConfidence(rule.Classification.Confidence),
			},
			Prompting: intakesvc.PromptingContext{
				SuggestedProfileID: strings.TrimSpace(rule.Prompting.SuggestedProfileID),
				TemplateID:         strings.TrimSpace(rule.Prompting.TemplateID),
			},
			Scope: intakesvc.ScopeContext{
				Values: renderActivationScope(rule.Scope.Values, vars),
			},
		}
		if activationRuleHasDirectAction(rule.Action) {
			input, ok := buildActivationActionInput(rule.Action, vars)
			if !ok {
				continue
			}
			override.DirectAction = intakesvc.DirectActionContext{
				ToolName:      strings.TrimSpace(rule.Action.Tool),
				Input:         input,
				AssistantText: renderActivationString(strings.TrimSpace(rule.Response.AssistantText), vars),
			}
		}
		return override
	}
	return nil
}

func activationRuleHasDirectAction(action agentmdl.ActivationAction) bool {
	if strings.TrimSpace(action.Tool) != "" {
		return true
	}
	if strings.TrimSpace(action.Foreach) != "" {
		return true
	}
	if len(action.Input) > 0 {
		return true
	}
	if len(action.Item) > 0 {
		return true
	}
	return false
}

func activationRulePatterns(match agentmdl.ActivationMatch) []string {
	var result []string
	if value := strings.TrimSpace(match.Pattern); value != "" {
		result = append(result, value)
	}
	for _, pattern := range match.Patterns {
		if value := strings.TrimSpace(pattern); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func compileActivationRegex(pattern, flags string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, fmt.Errorf("empty activation regex")
	}
	prefix := ""
	if strings.Contains(strings.ToLower(strings.TrimSpace(flags)), "i") {
		prefix = "(?i)"
	}
	return regexp.Compile(prefix + pattern)
}

func activationConfidence(value float64) float64 {
	if value > 0 {
		return value
	}
	return 1
}

func buildActivationVariables(query string, matches []string, extractors map[string]agentmdl.ActivationExtractor) (map[string]interface{}, bool) {
	vars := map[string]interface{}{
		"query": query,
	}
	for i, value := range matches {
		vars[fmt.Sprintf("%d", i)] = value
	}
	for name, extractor := range extractors {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		value, ok := runActivationExtractor(extractor, vars)
		if !ok {
			return nil, false
		}
		vars[key] = value
	}
	return vars, true
}

func runActivationExtractor(extractor agentmdl.ActivationExtractor, vars map[string]interface{}) (interface{}, bool) {
	switch strings.ToLower(strings.TrimSpace(extractor.Type)) {
	case "regex_all":
		source := renderActivationString(strings.TrimSpace(extractor.Source), vars)
		pattern := strings.TrimSpace(extractor.Pattern)
		if source == "" || pattern == "" {
			return nil, false
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, false
		}
		found := re.FindAllString(source, -1)
		if len(found) == 0 {
			return nil, false
		}
		unique := make([]string, 0, len(found))
		seen := map[string]struct{}{}
		for _, entry := range found {
			value := strings.TrimSpace(entry)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			unique = append(unique, value)
		}
		if len(unique) == 0 {
			return nil, false
		}
		return unique, true
	default:
		return nil, false
	}
}

func renderActivationScope(scope map[string]string, vars map[string]interface{}) map[string]string {
	if len(scope) == 0 {
		return nil
	}
	out := make(map[string]string, len(scope))
	for key, value := range scope {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		out[trimmedKey] = renderActivationString(value, vars)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildActivationActionInput(action agentmdl.ActivationAction, vars map[string]interface{}) (map[string]interface{}, bool) {
	input := map[string]interface{}{}
	for key, value := range action.Input {
		input[key] = renderActivationValue(value, vars, nil)
	}
	foreachKey := strings.TrimSpace(action.Foreach)
	if foreachKey == "" {
		if len(input) == 0 {
			return nil, false
		}
		return input, true
	}
	itemsRaw, ok := vars[foreachKey]
	if !ok {
		return nil, false
	}
	itemsList := toActivationList(itemsRaw)
	if len(itemsList) == 0 {
		return nil, false
	}
	renderedItems := make([]interface{}, 0, len(itemsList))
	for _, item := range itemsList {
		itemVars := map[string]interface{}{}
		for key, value := range vars {
			itemVars[key] = value
		}
		itemVars["item"] = item
		renderedItems = append(renderedItems, renderActivationValue(action.Item, itemVars, nil))
	}
	input["items"] = renderedItems
	return input, true
}

func toActivationList(value interface{}) []interface{} {
	switch actual := value.(type) {
	case []interface{}:
		return append([]interface{}{}, actual...)
	case []string:
		result := make([]interface{}, 0, len(actual))
		for _, entry := range actual {
			result = append(result, entry)
		}
		return result
	default:
		if actual == nil {
			return nil
		}
		return []interface{}{actual}
	}
}

var activationExactPlaceholderRE = regexp.MustCompile(`^\$([A-Za-z0-9_]+)(?::([A-Za-z]+))?$`)
var activationInlinePlaceholderRE = regexp.MustCompile(`\$([A-Za-z0-9_]+)`)

func renderActivationValue(value interface{}, vars map[string]interface{}, fallback interface{}) interface{} {
	switch actual := value.(type) {
	case string:
		if matches := activationExactPlaceholderRE.FindStringSubmatch(strings.TrimSpace(actual)); len(matches) > 0 {
			name := strings.TrimSpace(matches[1])
			if resolved, ok := vars[name]; ok {
				return coerceActivationValue(resolved, strings.TrimSpace(matches[2]))
			}
			return fallback
		}
		return renderActivationString(actual, vars)
	case []interface{}:
		out := make([]interface{}, 0, len(actual))
		for _, entry := range actual {
			out = append(out, renderActivationValue(entry, vars, nil))
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(actual))
		for key, entry := range actual {
			out[key] = renderActivationValue(entry, vars, nil)
		}
		return out
	default:
		return actual
	}
}

func renderActivationString(template string, vars map[string]interface{}) string {
	return activationInlinePlaceholderRE.ReplaceAllStringFunc(template, func(match string) string {
		groups := activationInlinePlaceholderRE.FindStringSubmatch(match)
		if len(groups) != 2 {
			return match
		}
		if value, ok := vars[strings.TrimSpace(groups[1])]; ok {
			return activationStringValue(value)
		}
		return match
	})
}

func coerceActivationValue(value interface{}, coercion string) interface{} {
	switch strings.ToLower(strings.TrimSpace(coercion)) {
	case "int":
		return mustAtoi(activationStringValue(value))
	default:
		return value
	}
}

func activationStringValue(value interface{}) string {
	switch actual := value.(type) {
	case nil:
		return ""
	case string:
		return actual
	case []string:
		return strings.Join(actual, ",")
	case []interface{}:
		parts := make([]string, 0, len(actual))
		for _, entry := range actual {
			parts = append(parts, activationStringValue(entry))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprintf("%v", actual)
	}
}

type activationFollowUpState struct {
	Source         string
	ClientID       string
	WindowID       string
	WindowKey      string
	WindowTitle    string
	WindowOrderIDs []string
	LiveWindow     *uireg.WindowSnapshot
}

func (s *Service) resolveWorkspaceFollowUpRuleOverride(ctx context.Context, conversationID, query string, rules []agentmdl.ActivationRule) *intakesvc.Context {
	if len(rules) == 0 {
		return nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	states := s.collectActivationFollowUpStates(ctx, conversationID)
	if len(states) == 0 {
		return nil
	}
	for _, rule := range rules {
		if strings.ToLower(strings.TrimSpace(rule.Mode)) != "followup" {
			continue
		}
		if override := evaluateFollowUpRule(query, rule, states); override != nil {
			return override
		}
	}
	return nil
}

func (s *Service) collectActivationFollowUpStates(ctx context.Context, conversationID string) []activationFollowUpState {
	var result []activationFollowUpState
	if s != nil && s.uiRegistry != nil {
		if client, activeWindow := liveWindowState(s.uiRegistry, ctx, conversationID); client != nil {
			if activeWindow != nil {
				result = append(result, activationFollowUpState{
					Source:         "live_window",
					ClientID:       strings.TrimSpace(client.ClientID),
					WindowID:       strings.TrimSpace(activeWindow.WindowID),
					WindowKey:      strings.TrimSpace(activeWindow.WindowKey),
					WindowTitle:    strings.TrimSpace(activeWindow.WindowTitle),
					WindowOrderIDs: extractWindowOrderIDs(activeWindow),
					LiveWindow:     activeWindow,
				})
			}
			if client.Snapshot != nil {
				for i := range client.Snapshot.Windows {
					win := &client.Snapshot.Windows[i]
					if activeWindow != nil && strings.TrimSpace(win.WindowID) == strings.TrimSpace(activeWindow.WindowID) {
						continue
					}
					if strings.EqualFold(strings.TrimSpace(win.WindowKey), "chat/new") {
						continue
					}
					result = append(result, activationFollowUpState{
						Source:         "live_window",
						ClientID:       strings.TrimSpace(client.ClientID),
						WindowID:       strings.TrimSpace(win.WindowID),
						WindowKey:      strings.TrimSpace(win.WindowKey),
						WindowTitle:    strings.TrimSpace(win.WindowTitle),
						WindowOrderIDs: extractWindowOrderIDs(win),
						LiveWindow:     win,
					})
				}
			}
		}
	}
	if s != nil && s.conversation != nil {
		if conversation, err := s.conversation.GetConversation(ctx, strings.TrimSpace(conversationID), apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true)); err == nil && conversation != nil {
			if state := deriveWorkspaceFollowUpStateFromTranscript(conversation.GetTranscript()); state != nil && strings.TrimSpace(state.WindowID) != "" {
				result = append(result, activationFollowUpState{
					Source:         "transcript_window",
					ClientID:       strings.TrimSpace(state.ClientID),
					WindowID:       strings.TrimSpace(state.WindowID),
					WindowKey:      strings.TrimSpace(state.WindowKey),
					WindowOrderIDs: append([]string(nil), state.OrderIDs...),
				})
			}
		}
	}
	return result
}

func evaluateFollowUpRule(query string, rule agentmdl.ActivationRule, states []activationFollowUpState) *intakesvc.Context {
	patterns := activationRulePatterns(rule.Match)
	if len(patterns) == 0 {
		return nil
	}
	for _, pattern := range patterns {
		re, err := compileActivationRegex(pattern, rule.Match.Flags)
		if err != nil {
			continue
		}
		matches := re.FindStringSubmatch(query)
		if len(matches) == 0 {
			continue
		}
		vars, ok := buildActivationVariables(query, matches, rule.Match.Extractors)
		if !ok {
			continue
		}
		filtered := filterFollowUpStates(states, rule)
		for _, state := range filtered {
			override := buildFollowUpOverrideFromState(rule, vars, state, filtered, query)
			if override != nil {
				return override
			}
		}
	}
	return nil
}

func filterFollowUpStates(states []activationFollowUpState, rule agentmdl.ActivationRule) []activationFollowUpState {
	source := strings.ToLower(strings.TrimSpace(rule.Source))
	windowKey := strings.TrimSpace(rule.WindowKey)
	var result []activationFollowUpState
	for _, state := range states {
		if source == "live_window" && state.Source != "live_window" {
			continue
		}
		if source == "transcript_window" && state.Source != "transcript_window" {
			continue
		}
		if windowKey != "" && strings.TrimSpace(state.WindowKey) != windowKey {
			continue
		}
		result = append(result, state)
	}
	return result
}

func buildFollowUpOverrideFromState(rule agentmdl.ActivationRule, vars map[string]interface{}, state activationFollowUpState, relatedStates []activationFollowUpState, query string) *intakesvc.Context {
	stateVars := map[string]interface{}{}
	for key, value := range vars {
		stateVars[key] = value
	}
	stateVars["windowId"] = state.WindowID
	stateVars["windowKey"] = state.WindowKey
	stateVars["windowTitle"] = state.WindowTitle
	stateVars["clientId"] = state.ClientID
	if len(state.WindowOrderIDs) > 0 {
		stateVars["windowOrderIds"] = strings.Join(state.WindowOrderIDs, ",")
	}
	if related := collectFollowUpOrderIDs(relatedStates); len(related) > 0 {
		stateVars["compareOrderIds"] = strings.Join(related, ",")
	}

	var direct *workspaceFollowUpDirectAction
	if rule.SurfaceMatch != nil {
		direct = resolveSurfaceMatchAction(rule, stateVars, state)
		if direct == nil {
			return nil
		}
	} else if strings.TrimSpace(rule.Action.Tool) != "" {
		input, ok := buildActivationActionInput(rule.Action, stateVars)
		if !ok {
			return nil
		}
		direct = &workspaceFollowUpDirectAction{
			ToolName:      strings.TrimSpace(rule.Action.Tool),
			Input:         input,
			AssistantText: renderActivationString(strings.TrimSpace(rule.Response.AssistantText), stateVars),
		}
	}
	if direct == nil && strings.TrimSpace(rule.Prompting.SuggestedProfileID) == "" && len(rule.Scope.Values) == 0 {
		return nil
	}
	if direct != nil && strings.TrimSpace(direct.AssistantText) == "" {
		direct.AssistantText = renderActivationString(strings.TrimSpace(rule.Response.AssistantText), stateVars)
	}
	scopeValues := renderActivationScope(rule.Scope.Values, stateVars)
	override := &intakesvc.Context{
		Classification: intakesvc.ClassificationContext{
			Title:      strings.TrimSpace(query),
			Intent:     firstNonEmpty(strings.TrimSpace(rule.Classification.Intent), "summary"),
			Confidence: activationConfidence(rule.Classification.Confidence),
		},
		Prompting: intakesvc.PromptingContext{
			SuggestedProfileID: strings.TrimSpace(rule.Prompting.SuggestedProfileID),
			TemplateID:         strings.TrimSpace(rule.Prompting.TemplateID),
		},
		Scope: intakesvc.ScopeContext{
			Values: scopeValues,
		},
	}
	if direct != nil {
		override.DirectAction = intakesvc.DirectActionContext{
			ToolName:      direct.ToolName,
			Input:         direct.Input,
			AssistantText: direct.AssistantText,
		}
	}
	return override
}

func collectFollowUpOrderIDs(states []activationFollowUpState) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0)
	for _, state := range states {
		for _, id := range state.WindowOrderIDs {
			trimmed := strings.TrimSpace(id)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			result = append(result, trimmed)
		}
	}
	return result
}

func extractWindowOrderIDs(win *uireg.WindowSnapshot) []string {
	if win == nil {
		return nil
	}
	var collected []string
	appendRaw := func(raw interface{}) {
		switch actual := raw.(type) {
		case []interface{}:
			for _, item := range actual {
				value := ""
				switch typed := item.(type) {
				case float64:
					value = strings.TrimSpace(strconv.FormatInt(int64(typed), 10))
				default:
					value = activationStringValue(item)
				}
				if value != "" && value != "<nil>" {
					collected = append(collected, value)
				}
			}
		case []string:
			for _, item := range actual {
				value := strings.TrimSpace(item)
				if value != "" {
					collected = append(collected, value)
				}
			}
		default:
			value := ""
			switch typed := raw.(type) {
			case float64:
				value = strings.TrimSpace(strconv.FormatInt(int64(typed), 10))
			default:
				value = activationStringValue(raw)
			}
			if value != "" && value != "<nil>" {
				collected = append(collected, value)
			}
		}
	}
	if win.Parameters != nil {
		if raw, ok := win.Parameters["AdOrderId"]; ok {
			appendRaw(raw)
		}
	}
	if win.CompareContext != nil {
		if raw, ok := win.CompareContext["orderIds"]; ok {
			appendRaw(raw)
		}
	}
	for _, ds := range win.DataSources {
		if ds.Input == nil {
			continue
		}
		if raw, ok := ds.Input["order_id"]; ok {
			appendRaw(raw)
		}
		if raw, ok := ds.Input["AdOrderId"]; ok {
			appendRaw(raw)
		}
	}
	if len(collected) == 0 {
		for _, item := range extractOrderIDs(firstNonEmpty(strings.TrimSpace(win.WindowTitle), strings.TrimSpace(win.WindowID))) {
			collected = append(collected, item)
		}
	}
	if len(collected) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(collected))
	for _, item := range collected {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func resolveSurfaceMatchAction(rule agentmdl.ActivationRule, vars map[string]interface{}, state activationFollowUpState) *workspaceFollowUpDirectAction {
	if rule.SurfaceMatch == nil {
		return nil
	}
	target := strings.TrimSpace(activationStringValue(firstNonEmptyVar(vars, "target", "1")))
	if target == "" {
		return nil
	}
	normalizedTarget := normalizeSurfaceToken(target)
	if normalizedTarget == "" {
		return nil
	}
	if aliasTabID, ok := matchSurfaceTabAlias(rule.SurfaceMatch.TabAliases, normalizedTarget); ok {
		if state.LiveWindow != nil {
			surface := uireg.BuildWindowSurface(state.LiveWindow)
			if surface != nil {
				for _, tab := range surface.Tabs {
					if strings.TrimSpace(tab.TabID) == aliasTabID {
						return buildSelectTabAction(state.ClientID, state.LiveWindow, tab)
					}
				}
			}
		}
		return &workspaceFollowUpDirectAction{
			ToolName: "ui/window:selectTab",
			Input: map[string]interface{}{
				"windowId":  state.WindowID,
				"windowKey": state.WindowKey,
				"tabId":     aliasTabID,
				"clientId":  optionalStringInput(state.ClientID),
			},
			AssistantText: renderActivationString(strings.TrimSpace(rule.Response.AssistantText), vars),
		}
	}
	if aliasControl, ok := matchSurfaceControlAlias(rule.SurfaceMatch.ControlAliases, normalizedTarget); ok {
		input := map[string]interface{}{
			"windowId":  state.WindowID,
			"windowKey": state.WindowKey,
			"controlId": aliasControl.ControlID,
			"value":     aliasControl.Value,
		}
		if state.ClientID != "" {
			input["clientId"] = state.ClientID
		}
		return &workspaceFollowUpDirectAction{
			ToolName:      "ui/control:setValue",
			Input:         input,
			AssistantText: renderActivationString(strings.TrimSpace(rule.Response.AssistantText), vars),
		}
	}
	if state.LiveWindow == nil {
		return nil
	}
	surface := uireg.BuildWindowSurface(state.LiveWindow)
	if surface == nil {
		return nil
	}
	allowedTabs := normalizedStringSet(rule.SurfaceMatch.Tabs)
	for _, tab := range surface.Tabs {
		if len(allowedTabs) > 0 && !allowedTabs[strings.TrimSpace(tab.TabID)] {
			continue
		}
		if normalizeSurfaceToken(tab.Title) == normalizedTarget || normalizeSurfaceToken(tab.TabID) == normalizedTarget {
			return buildSelectTabAction(state.ClientID, state.LiveWindow, tab)
		}
	}
	allowedControls := normalizedStringSet(rule.SurfaceMatch.Controls)
	for _, control := range surface.Controls {
		if len(allowedControls) > 0 && !allowedControls[strings.TrimSpace(control.ID)] {
			continue
		}
		for _, option := range control.Options {
			valueLabel := firstNonEmpty(strings.TrimSpace(option.Label), fmt.Sprint(option.Value))
			if normalizeSurfaceToken(valueLabel) == normalizedTarget || normalizeSurfaceToken(fmt.Sprint(option.Value)) == normalizedTarget {
				return buildSetControlAction(state.ClientID, state.LiveWindow, control, option.Value, valueLabel)
			}
		}
	}
	return nil
}

func normalizedStringSet(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	result := map[string]bool{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result[trimmed] = true
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func matchSurfaceTabAlias(aliases map[string]string, target string) (string, bool) {
	for key, value := range aliases {
		if normalizeSurfaceToken(key) == target {
			return strings.TrimSpace(value), strings.TrimSpace(value) != ""
		}
	}
	return "", false
}

func matchSurfaceControlAlias(aliases map[string]agentmdl.ActivationSurfaceControlAlias, target string) (agentmdl.ActivationSurfaceControlAlias, bool) {
	for key, value := range aliases {
		if normalizeSurfaceToken(key) == target {
			return value, strings.TrimSpace(value.ControlID) != ""
		}
	}
	return agentmdl.ActivationSurfaceControlAlias{}, false
}

func firstNonEmptyVar(vars map[string]interface{}, keys ...string) interface{} {
	for _, key := range keys {
		if value, ok := vars[key]; ok {
			if strings.TrimSpace(activationStringValue(value)) != "" {
				return value
			}
		}
	}
	return nil
}

func optionalStringInput(value string) interface{} {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

type workspaceFollowUpState struct {
	WindowID  string
	WindowKey string
	ClientID  string
	OrderIDs  []string
}

type workspaceFollowUpDirectAction struct {
	ToolName      string
	Input         map[string]interface{}
	AssistantText string
}

func liveWindowState(reg *uireg.Registry, ctx context.Context, conversationID string) (*uireg.ClientSnapshot, *uireg.WindowSnapshot) {
	if reg == nil {
		return nil, nil
	}
	conversationID = strings.TrimSpace(conversationID)
	preferredClientID := normalizeOptionalClientID(strings.TrimSpace(runtimerequestctx.PreferredUIClientIDFromContext(ctx)))
	items, err := reg.ListByConversation(ctx, conversationID)
	if err != nil || len(items) == 0 {
		return nil, nil
	}
	if preferredClientID != "" {
		for _, item := range items {
			if strings.TrimSpace(item.ClientID) == preferredClientID {
				items = []uireg.ClientSnapshot{item}
				break
			}
		}
	}
	client := items[0]
	if client.Snapshot == nil {
		return &client, nil
	}
	selectedWindowID := strings.TrimSpace(client.Snapshot.Selected.WindowID)
	for i := range client.Snapshot.Windows {
		if strings.TrimSpace(client.Snapshot.Windows[i].WindowID) == selectedWindowID {
			return &client, &client.Snapshot.Windows[i]
		}
	}
	return &client, nil
}

func normalizeOptionalClientID(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "default") {
		return ""
	}
	return value
}

func normalizedWorkspaceIntent(query string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(query)), " "))
}

func matchesAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

var orderIDPattern = regexp.MustCompile(`\b\d{4,}\b`)

func extractOrderIDs(query string) []string {
	found := orderIDPattern.FindAllString(query, -1)
	if len(found) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(found))
	for _, item := range found {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func normalizeSurfaceToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	value = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(value, " ")
	value = strings.Join(strings.Fields(value), " ")
	if strings.HasSuffix(value, "s") && len(value) > 3 {
		value = strings.TrimSuffix(value, "s")
	}
	return value
}

func buildSelectTabAction(clientID string, win *uireg.WindowSnapshot, tab uireg.SurfaceTab) *workspaceFollowUpDirectAction {
	if win == nil {
		return nil
	}
	assistantText := fmt.Sprintf("Switched the open %s workspace to the %s tab.", firstNonEmpty(strings.TrimSpace(win.WindowTitle), strings.TrimSpace(win.WindowKey), "workspace"), firstNonEmpty(strings.TrimSpace(tab.Title), strings.TrimSpace(tab.TabID), "selected"))
	input := map[string]interface{}{
		"windowId": strings.TrimSpace(win.WindowID),
		"tabId":    strings.TrimSpace(tab.TabID),
	}
	if clientID = strings.TrimSpace(clientID); clientID != "" {
		input["clientId"] = clientID
	}
	return &workspaceFollowUpDirectAction{
		ToolName:      "ui/window:selectTab",
		Input:         input,
		AssistantText: assistantText,
	}
}

func buildSetControlAction(clientID string, win *uireg.WindowSnapshot, control uireg.SurfaceControl, value interface{}, valueLabel string) *workspaceFollowUpDirectAction {
	if win == nil {
		return nil
	}
	assistantText := fmt.Sprintf("Updated %s to %s on the open %s workspace.", firstNonEmpty(strings.TrimSpace(control.Label), strings.TrimSpace(control.ID), "control"), firstNonEmpty(strings.TrimSpace(valueLabel), fmt.Sprint(value)), firstNonEmpty(strings.TrimSpace(win.WindowTitle), strings.TrimSpace(win.WindowKey), "workspace"))
	input := map[string]interface{}{
		"windowId":    strings.TrimSpace(win.WindowID),
		"controlId":   strings.TrimSpace(control.ID),
		"scope":       strings.TrimSpace(control.Scope),
		"value":       value,
		"bindingPath": strings.TrimSpace(control.BindingPath),
		"dataField":   strings.TrimSpace(control.DataField),
	}
	if clientID = strings.TrimSpace(clientID); clientID != "" {
		input["clientId"] = clientID
	}
	return &workspaceFollowUpDirectAction{
		ToolName:      "ui/control:setValue",
		Input:         input,
		AssistantText: assistantText,
	}
}

type transcriptToolStep struct {
	ToolName        string
	Status          string
	Content         string
	RequestPayload  *agconv.ModelCallStreamPayloadView
	ResponsePayload *agconv.ModelCallStreamPayloadView
	CreatedAt       time.Time
	Sequence        int
}

func deriveWorkspaceFollowUpStateFromTranscript(transcript apiconv.Transcript) *workspaceFollowUpState {
	if len(transcript) == 0 {
		return nil
	}
	lastTurn := transcript[len(transcript)-1]
	if lastTurn == nil {
		return nil
	}
	steps := collectTranscriptToolSteps(lastTurn)
	if len(steps) == 0 {
		return nil
	}
	windowsByID := map[string]string{}
	selectedWindowID := ""
	clientID := ""
	for _, step := range steps {
		if !strings.EqualFold(strings.TrimSpace(step.Status), "completed") {
			continue
		}
		toolName := normalizeWorkspaceToolName(step.ToolName)
		switch toolName {
		case "ui/view/open":
			payload := firstWorkspacePayloadMap(step.Content, step.ResponsePayload)
			indexWorkspaceWindows(payload, windowsByID)
			if id := strings.TrimSpace(stringValue(payload["selectedWindowId"])); id != "" {
				selectedWindowID = id
			} else if id := strings.TrimSpace(stringValue(payload["windowId"])); id != "" {
				selectedWindowID = id
			}
			if clientID == "" {
				clientID = firstNonEmpty(
					strings.TrimSpace(stringValue(payload["clientId"])),
					strings.TrimSpace(stringValue(payloadBodyMap(step.RequestPayload)["clientId"])),
				)
			}
		case "ui/window/list":
			payload := firstWorkspacePayloadMap(step.Content, step.ResponsePayload)
			indexWorkspaceWindows(payload, windowsByID)
			if id := strings.TrimSpace(stringValue(payload["focusedWindowId"])); id != "" {
				selectedWindowID = id
			}
			if clientID == "" {
				clientID = firstNonEmpty(
					strings.TrimSpace(stringValue(payload["clientId"])),
					strings.TrimSpace(stringValue(payloadBodyMap(step.RequestPayload)["clientId"])),
				)
			}
		case "ui/window/show", "ui/window/selecttab", "ui/control/setvalue":
			requestPayload := payloadBodyMap(step.RequestPayload)
			if id := strings.TrimSpace(stringValue(requestPayload["windowId"])); id != "" {
				selectedWindowID = id
				if key := strings.TrimSpace(stringValue(requestPayload["windowKey"])); key != "" {
					windowsByID[id] = key
				}
			}
			if clientID == "" {
				clientID = strings.TrimSpace(stringValue(requestPayload["clientId"]))
			}
		}
	}
	selectedWindowID = strings.TrimSpace(selectedWindowID)
	if selectedWindowID == "" {
		return nil
	}
	windowKey := strings.TrimSpace(windowsByID[selectedWindowID])
	if windowKey == "" {
		return nil
	}
	return &workspaceFollowUpState{
		WindowID:  selectedWindowID,
		WindowKey: windowKey,
		ClientID:  strings.TrimSpace(clientID),
	}
}

func collectTranscriptToolSteps(turn *apiconv.Turn) []transcriptToolStep {
	if turn == nil || len(turn.Message) == 0 {
		return nil
	}
	result := make([]transcriptToolStep, 0)
	for _, message := range turn.Message {
		if message == nil || len(message.ToolMessage) == 0 {
			continue
		}
		for _, toolMessage := range message.ToolMessage {
			if toolMessage == nil || toolMessage.ToolCall == nil {
				continue
			}
			seq := 0
			if toolMessage.Sequence != nil {
				seq = *toolMessage.Sequence
			}
			result = append(result, transcriptToolStep{
				ToolName:        strings.TrimSpace(toolMessage.ToolCall.ToolName),
				Status:          strings.TrimSpace(toolMessage.ToolCall.Status),
				Content:         strings.TrimSpace(valueOrEmpty(toolMessage.Content)),
				RequestPayload:  toolMessage.ToolCall.RequestPayload,
				ResponsePayload: toolMessage.ToolCall.ResponsePayload,
				CreatedAt:       toolMessage.CreatedAt,
				Sequence:        seq,
			})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].Sequence < result[j].Sequence
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}

func normalizeWorkspaceToolName(raw string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), ":", "/"))
}

func payloadBodyMap(payload *agconv.ModelCallStreamPayloadView) map[string]interface{} {
	if payload == nil || payload.InlineBody == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(payload.Compression), "none") && strings.TrimSpace(payload.Compression) != "" {
		return nil
	}
	return parseWorkspacePayload(strings.TrimSpace(*payload.InlineBody))
}

func firstWorkspacePayloadMap(content string, payload *agconv.ModelCallStreamPayloadView) map[string]interface{} {
	if parsed := parseWorkspacePayload(strings.TrimSpace(content)); len(parsed) > 0 {
		return parsed
	}
	return payloadBodyMap(payload)
}

func parseWorkspacePayload(raw string) map[string]interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func indexWorkspaceWindows(payload map[string]interface{}, windowsByID map[string]string) {
	if len(payload) == 0 {
		return
	}
	if items, ok := payload["items"].([]interface{}); ok {
		for _, raw := range items {
			entry, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			windowID := strings.TrimSpace(stringValue(entry["windowId"]))
			windowKey := strings.TrimSpace(stringValue(entry["windowKey"]))
			if windowID == "" || windowKey == "" {
				continue
			}
			windowsByID[windowID] = windowKey
		}
	}
	windowID := strings.TrimSpace(stringValue(payload["windowId"]))
	windowKey := strings.TrimSpace(stringValue(payload["windowKey"]))
	if windowID != "" && windowKey != "" {
		windowsByID[windowID] = windowKey
	}
}

func (s *Service) normalizeIntakeTurnContext(ctx context.Context, input *QueryInput, tc *intakesvc.Context, cfg *agentmdl.Intake) {
	if s == nil || input == nil || tc == nil || cfg == nil {
		return
	}
	if !cfg.HasScope(agentmdl.IntakeScopeProfile) {
		return
	}
	suggested := strings.TrimSpace(tc.Prompting.SuggestedProfileID)
	if suggested == "" {
		return
	}
	if s.isAllowedIntakePromptProfile(ctx, input, suggested) {
		return
	}
	logx.Infof("conversation", "intake.invalid_profile_suppressed convo=%q agent=%q suggested=%q",
		strings.TrimSpace(input.ConversationID),
		strings.TrimSpace(input.Agent.ID),
		suggested,
	)
	tc.Prompting.SuggestedProfileID = ""
	tc.Classification.Confidence = 0
}

func (s *Service) isAllowedIntakePromptProfile(ctx context.Context, input *QueryInput, profileID string) bool {
	profileID = strings.TrimSpace(strings.ToLower(profileID))
	if profileID == "" || input == nil || input.Agent == nil {
		return false
	}
	if bundles := input.Agent.Prompts.Bundles; len(bundles) > 0 {
		for _, candidate := range bundles {
			if strings.TrimSpace(strings.ToLower(candidate)) == profileID {
				return true
			}
		}
		return false
	}
	if s.promptRepo == nil {
		return false
	}
	_, err := s.promptRepo.Load(ctx, profileID)
	return err == nil
}

func (s *Service) intakeTrackedContext(ctx context.Context, input *QueryInput) context.Context {
	if s == nil || input == nil {
		return ctx
	}
	preferredTurnID := ""
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		preferredTurnID = strings.TrimSpace(turn.TurnID)
	}
	runCtx := s.ensureRunTrackedLLMContext(ctx, strings.TrimSpace(input.ConversationID), "intake_sidecar", preferredTurnID)
	runCtx = runtimerequestctx.WithRequestMode(runCtx, "router")
	if input.Agent != nil && len(input.Agent.Prompts.Bundles) > 0 {
		runCtx = runtimerequestctx.WithPromptProfileAllowList(runCtx, input.Agent.Prompts.Bundles)
	}
	return runCtx
}

// shouldRunIntake decides whether the sidecar should fire for this turn.
//
// Behaviour:
//   - TriggerOnTopicShift == false → always run when the sidecar is enabled
//     (legacy default; the sidecar is cheap and every turn benefits).
//   - TriggerOnTopicShift == true  → run on the first turn of a conversation,
//     and on subsequent turns only when the current user message diverges
//     from the previous one by more than TopicShiftThreshold. Divergence is
//     measured as 1 − Jaccard(tokens(current), tokens(prev)).
//
// The Jaccard heuristic is cheap, deterministic, and good enough to spot the
// usual "user pivoted to a completely different task" case without paying
// for an embedding model. Threshold defaults to 0.65.
func (s *Service) shouldRunIntake(ctx context.Context, input *QueryInput, cfg *agentmdl.Intake) bool {
	if cfg == nil || !cfg.Enabled {
		return false
	}
	if input != nil && strings.TrimSpace(input.PromptProfileId) != "" {
		// Explicit prompt-profile selection is already a resolved routing
		// contract for this turn (commonly delegated child runs). Re-running
		// agent intake would duplicate classification work and can produce
		// conflicting or empty sidecar output without adding new information.
		return false
	}
	if !cfg.TriggerOnTopicShift {
		return true
	}
	current := strings.TrimSpace(input.Query)
	if current == "" {
		return true
	}
	if isConcreteOrderOpenAsk(current) {
		return true
	}
	previous := s.previousUserMessage(ctx, input.ConversationID)
	if previous == "" {
		// First turn — no prior user message to compare against; run so the
		// caller gets baseline Class A metadata.
		return true
	}
	threshold := cfg.TopicShiftThreshold
	if threshold <= 0 {
		threshold = 0.65
	}
	similarity := jaccardWordSimilarity(previous, current)
	divergence := 1.0 - similarity
	if divergence < threshold && s.priorMatchingTurnHadDirectAction(ctx, input.ConversationID, current) {
		return true
	}
	return divergence >= threshold
}

var concreteOrderOpenAskPattern = regexp.MustCompile(`(?i)^\s*(show|open)\s+(my\s+|ad\s+)?order\s+\d+\s*$`)
var concreteOrderCompareAskPattern = regexp.MustCompile(`(?i)^\s*(show|open)\s+((me|my|ad)\s+)?orders?\b`)

func isConcreteOrderOpenAsk(query string) bool {
	return concreteOrderOpenAskPattern.MatchString(strings.TrimSpace(query))
}

func isConcreteOrderActivationAsk(query string) bool {
	trimmed := strings.TrimSpace(query)
	if isConcreteOrderOpenAsk(trimmed) {
		return true
	}
	if !concreteOrderCompareAskPattern.MatchString(trimmed) {
		return false
	}
	return len(extractOrderIDs(trimmed)) >= 2
}

// previousUserMessage returns the trimmed content of the most recent user
// message on the conversation, excluding the current turn's message. Empty
// result means "no prior history available" — caller treats that as first
// turn.
func (s *Service) previousUserMessage(ctx context.Context, convID string) string {
	if s == nil || s.conversation == nil {
		return ""
	}
	convID = strings.TrimSpace(convID)
	if convID == "" {
		return ""
	}
	conv, err := s.conversation.GetConversation(ctx, convID, apiconv.WithIncludeTranscript(true))
	if err != nil || conv == nil {
		return ""
	}
	turns := conv.GetTranscript()
	// Walk backwards and pick the newest user message. The tail of the
	// transcript may be the turn we're currently starting — skip any
	// assistant-only tail and grab the latest persisted user input.
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		if turn == nil {
			continue
		}
		for j := len(turn.Message) - 1; j >= 0; j-- {
			msg := turn.Message[j]
			if msg == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
				if msg.Content != nil {
					if text := strings.TrimSpace(*msg.Content); text != "" {
						return text
					}
				}
			}
		}
	}
	return ""
}

func (s *Service) priorMatchingTurnHadDirectAction(ctx context.Context, convID string, current string) bool {
	if s == nil || s.conversation == nil {
		return false
	}
	convID = strings.TrimSpace(convID)
	if convID == "" {
		return false
	}
	conv, err := s.conversation.GetConversation(ctx, convID, apiconv.WithIncludeTranscript(true))
	if err != nil || conv == nil {
		return false
	}
	turns := conv.GetTranscript()
	current = strings.TrimSpace(current)
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		if turn == nil || len(turn.Message) == 0 {
			continue
		}
		var userText string
		var intakeDirectAction bool
		for _, msg := range turn.Message {
			if msg == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
				if msg.Content != nil && strings.TrimSpace(*msg.Content) != "" {
					userText = strings.TrimSpace(*msg.Content)
				}
			}
			if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
				continue
			}
			if msg.Phase == nil || !strings.EqualFold(strings.TrimSpace(*msg.Phase), "intake") {
				continue
			}
			if msg.Content == nil || strings.TrimSpace(*msg.Content) == "" {
				continue
			}
			var tc intakesvc.Context
			if err := json.Unmarshal([]byte(strings.TrimSpace(*msg.Content)), &tc); err != nil {
				continue
			}
			intakeDirectAction = strings.TrimSpace(tc.DirectAction.ToolName) != ""
		}
		if userText == "" {
			continue
		}
		if strings.EqualFold(userText, current) && intakeDirectAction {
			return true
		}
	}
	return false
}

// jaccardWordSimilarity returns |A ∩ B| / |A ∪ B| over lowercased word
// tokens. Empty inputs → 0.
func jaccardWordSimilarity(a, b string) float64 {
	aTokens := tokenizeWords(a)
	bTokens := tokenizeWords(b)
	if len(aTokens) == 0 || len(bTokens) == 0 {
		return 0
	}
	union := make(map[string]struct{}, len(aTokens)+len(bTokens))
	intersection := 0
	for tok := range aTokens {
		union[tok] = struct{}{}
		if _, ok := bTokens[tok]; ok {
			intersection++
		}
	}
	for tok := range bTokens {
		union[tok] = struct{}{}
	}
	if len(union) == 0 {
		return 0
	}
	return float64(intersection) / float64(len(union))
}

// tokenizeWords lowercases the input and splits on any non-letter / non-digit
// rune. Tokens shorter than 2 runes are dropped — they are usually
// punctuation residue or single-letter noise that pollutes the overlap.
func tokenizeWords(s string) map[string]struct{} {
	out := map[string]struct{}{}
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		token := strings.ToLower(b.String())
		b.Reset()
		if len([]rune(token)) < 2 {
			return
		}
		out[token] = struct{}{}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// maybeSetConversationTitle persists the intake-extracted title to the
// conversation store and relies on PatchConversations emitting the
// conversation_meta_updated SSE event so connected clients update their
// sidebar / header without polling.
func (s *Service) maybeSetConversationTitle(ctx context.Context, convID, title string) {
	title = strings.TrimSpace(title)
	convID = strings.TrimSpace(convID)
	if title == "" || convID == "" || s == nil || s.conversation == nil {
		return
	}
	patch := apiconv.NewConversation()
	patch.SetId(convID)
	patch.SetTitle(title)
	if err := s.conversation.PatchConversations(ctx, patch); err != nil {
		logx.Warnf("conversation", "intake: set title convo=%q err=%v", convID, err)
	}
}

// applyTurnContext writes intake Context fields back into QueryInput so the
// downstream pipeline can use them.
func applyTurnContext(input *QueryInput, tc *intakesvc.Context, cfg *agentmdl.Intake) {
	if input == nil || tc == nil {
		return
	}
	if input.Context == nil {
		input.Context = make(map[string]interface{})
	}

	if existing, ok := input.Context[intakesvc.ContextKey].(*intakesvc.Context); ok && existing != nil && existing.Routing.Source == intakesvc.SourceWorkspace {
		tc.Routing.Mode = existing.Routing.Mode
		tc.Planner.Trigger = existing.Planner.Trigger
		tc.Planner.AgentID = existing.Planner.AgentID
		tc.Routing.SelectedAgentID = existing.Routing.SelectedAgentID
		tc.Routing.Source = existing.Routing.Source
	}

	maybeEnablePlannerMode(input, tc, cfg)

	// Always store the full context under the well-known key.
	input.Context[intakesvc.ContextKey] = tc

	// Surface title for conversation labelling.
	if t := strings.TrimSpace(tc.Classification.Title); t != "" {
		input.Context["intake.title"] = t
	}

	// Merge intake context into QueryInput.Context so templates and routing can
	// access normalized scope hints without treating them as authoritative data.
	if len(tc.Scope.Values) > 0 {
		for k, v := range tc.Scope.Values {
			input.Context["intake.context."+k] = v
		}
		input.Context["intake.context"] = tc.Scope.Values
		// Backward-compatible alias for existing templates/workspaces.
		input.Context["intake.entities"] = tc.Scope.Values
	}

	// Class B: append tool bundles suggested by the sidecar.
	if cfg.HasScope(agentmdl.IntakeScopeTools) && len(tc.Prompting.AppendToolBundles) > 0 {
		input.ToolBundles = append(input.ToolBundles, tc.Prompting.AppendToolBundles...)
	}

	// Class B: apply template suggestion. The context entry remains for
	// observability, but we also populate input.TemplateId — the field
	// applySelectedTemplate actually reads — when the caller has not
	// already chosen a template. Never overwrite an explicit caller choice.
	if cfg.HasScope(agentmdl.IntakeScopeTemplate) {
		if id := strings.TrimSpace(tc.Prompting.TemplateID); id != "" {
			input.Context["intake.templateId"] = id
			if strings.TrimSpace(input.TemplateId) == "" {
				input.TemplateId = id
			}
		}
	}

	// Class B: store profile suggestion when confidence meets the threshold.
	// Profile selection is explicit turn state. We record it for observability
	// and populate QueryInput.PromptProfileId when the caller did not already
	// choose one.
	if cfg.HasScope(agentmdl.IntakeScopeProfile) && strings.TrimSpace(tc.Prompting.SuggestedProfileID) != "" {
		if tc.Classification.Confidence >= cfg.EffectiveConfidenceThreshold() {
			suggested := strings.TrimSpace(tc.Prompting.SuggestedProfileID)
			input.Context["intake.suggestedProfileId"] = suggested
			input.Context["intake.suggestedProfileConfidence"] = tc.Classification.Confidence
			if strings.TrimSpace(input.PromptProfileId) == "" {
				input.PromptProfileId = suggested
			}
		}
	}
}

func maybeEnablePlannerMode(input *QueryInput, tc *intakesvc.Context, cfg *agentmdl.Intake) {
	if input == nil || tc == nil || cfg == nil {
		return
	}
	if !cfg.PlannerEnabled {
		return
	}
	explicitMode := strings.TrimSpace(tc.Routing.Mode) == intakesvc.ModePlanner
	explicitTrigger := strings.TrimSpace(tc.Planner.Trigger)
	if !explicitMode && explicitTrigger == "" {
		return
	}
	tc.Routing.Mode = intakesvc.ModePlanner
	if strings.TrimSpace(tc.Routing.SelectedAgentID) == "" {
		tc.Routing.SelectedAgentID = strings.TrimSpace(input.AgentID)
		if strings.TrimSpace(tc.Routing.SelectedAgentID) == "" && input.Agent != nil {
			tc.Routing.SelectedAgentID = strings.TrimSpace(input.Agent.ID)
		}
	}
	if strings.TrimSpace(tc.Routing.Source) == "" {
		tc.Routing.Source = intakesvc.SourceAgent
	}
	if strings.TrimSpace(tc.Planner.AgentID) == "" {
		tc.Planner.AgentID = strings.TrimSpace(cfg.PlannerAgentID)
	}
	logx.Infof("conversation", "intake.planner.selected convo=%q agent=%q selectedAgent=%q trigger=%q source=%q",
		strings.TrimSpace(input.ConversationID),
		strings.TrimSpace(input.AgentID),
		strings.TrimSpace(tc.Routing.SelectedAgentID),
		strings.TrimSpace(tc.Planner.Trigger),
		strings.TrimSpace(tc.Routing.Source),
	)
}
