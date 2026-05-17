package agent

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/internal/logx"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	intakesvc "github.com/viant/agently-core/service/intake"
	planner "github.com/viant/agently-core/service/planner"
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

	if s.maybeInjectWorkspaceFollowUpDirectAction(ctx, input) {
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
	if !cfg.Enabled {
		return
	}
	logx.Infof("conversation", "intake.consider convo=%q agent=%q promptProfileId=%q triggerOnTopicShift=%v",
		strings.TrimSpace(input.ConversationID),
		strings.TrimSpace(input.Agent.ID),
		strings.TrimSpace(input.PromptProfileId),
		cfg.TriggerOnTopicShift,
	)
	if !s.shouldRunIntake(ctx, input, cfg) {
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
	tc := s.intakeSvc.Run(runCtx, userMessage, cfg, strings.TrimSpace(input.UserId))
	if tc == nil {
		return
	}
	s.normalizeIntakeTurnContext(ctx, input, tc, cfg)
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
	applyTurnContext(input, tc, cfg)
	s.maybeSetConversationTitle(ctx, input.ConversationID, tc.Classification.Title)
}

func (s *Service) maybeInjectWorkspaceFollowUpDirectAction(ctx context.Context, input *QueryInput) bool {
	if s == nil || input == nil || s.conversation == nil {
		return false
	}
	toolName, actionInput, assistantText, ok := s.resolveWorkspaceFollowUpDirectAction(ctx, input.ConversationID, input.Query)
	if !ok {
		return false
	}
	override := &intakesvc.Context{
		Classification: intakesvc.ClassificationContext{
			Title:      strings.TrimSpace(input.Query),
			Intent:     "summary",
			Confidence: 1,
		},
		DirectAction: intakesvc.DirectActionContext{
			ToolName:      toolName,
			Input:         actionInput,
			AssistantText: assistantText,
		},
	}
	input.Context, _ = intakesvc.StoreCallerProvided(input.Context, override)
	logx.Infof("conversation", "intake.workspace_followup_direct_action convo=%q tool=%q query=%q", strings.TrimSpace(input.ConversationID), toolName, strings.TrimSpace(input.Query))
	return true
}

func (s *Service) resolveWorkspaceFollowUpDirectAction(ctx context.Context, conversationID, query string) (string, map[string]interface{}, string, bool) {
	conversationID = strings.TrimSpace(conversationID)
	query = strings.TrimSpace(query)
	if conversationID == "" || query == "" {
		return "", nil, "", false
	}
	conversation, err := s.conversation.GetConversation(ctx, conversationID, apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
	if err != nil || conversation == nil {
		return "", nil, "", false
	}
	state := deriveWorkspaceFollowUpStateFromTranscript(conversation.GetTranscript())
	if state == nil || strings.TrimSpace(state.WindowID) == "" || strings.TrimSpace(state.WindowKey) != "order" {
		return "", nil, "", false
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	tabID := ""
	assistantText := ""
	switch normalized {
	case "show kpi", "show kpis":
		tabID = "kpiTab"
		assistantText = "Switched the open order summary to the KPIs tab."
	case "show delivery":
		tabID = "deliveryTab"
		assistantText = "Switched the open order summary to the Delivery tab."
	case "show hh metrics", "show household metrics":
		tabID = "hhMetricsTab"
		assistantText = "Switched the open order summary to the HH Metrics tab."
	case "show pacing":
		tabID = "pacingTab"
		assistantText = "Switched the open order summary to the Pacing tab."
	}
	if tabID != "" {
		input := map[string]interface{}{
			"windowId":  strings.TrimSpace(state.WindowID),
			"windowKey": "order",
			"tabId":     tabID,
		}
		if strings.TrimSpace(state.ClientID) != "" {
			input["clientId"] = strings.TrimSpace(state.ClientID)
		}
		return "ui/window/selectTab", input, assistantText, true
	}

	controlID := ""
	controlValue := ""
	switch normalized {
	case "show today":
		controlID = "periodView"
		controlValue = "today"
		assistantText = "Switched the open order summary period to Today."
	case "show yesterday":
		controlID = "periodView"
		controlValue = "yesterday"
		assistantText = "Switched the open order summary period to Yesterday."
	case "show 7d":
		controlID = "periodView"
		controlValue = "7d"
		assistantText = "Switched the open order summary period to 7D."
	case "show 30d":
		controlID = "periodView"
		controlValue = "30d"
		assistantText = "Switched the open order summary period to 30D."
	case "switch to hour", "show hour":
		controlID = "granularity"
		controlValue = "hour"
		assistantText = "Switched the open order summary granularity to Hour."
	case "switch to day", "show day":
		controlID = "granularity"
		controlValue = "day"
		assistantText = "Switched the open order summary granularity to Day."
	default:
		return "", nil, "", false
	}
	input := map[string]interface{}{
		"windowId":  strings.TrimSpace(state.WindowID),
		"windowKey": "order",
		"controlId": controlID,
		"scope":     "windowForm",
		"value":     controlValue,
	}
	if strings.TrimSpace(state.ClientID) != "" {
		input["clientId"] = strings.TrimSpace(state.ClientID)
	}
	return "ui/control:setValue", input, assistantText, true
}

type workspaceFollowUpState struct {
	WindowID  string
	WindowKey string
	ClientID  string
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

func isConcreteOrderOpenAsk(query string) bool {
	return concreteOrderOpenAskPattern.MatchString(strings.TrimSpace(query))
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
	if strings.TrimSpace(tc.Routing.Mode) == intakesvc.ModePlanner {
		if strings.TrimSpace(tc.Planner.AgentID) == "" {
			tc.Planner.AgentID = strings.TrimSpace(cfg.PlannerAgentID)
		}
		if strings.TrimSpace(tc.Routing.SelectedAgentID) == "" {
			tc.Routing.SelectedAgentID = strings.TrimSpace(input.AgentID)
			if strings.TrimSpace(tc.Routing.SelectedAgentID) == "" && input.Agent != nil {
				tc.Routing.SelectedAgentID = strings.TrimSpace(input.Agent.ID)
			}
		}
		if strings.TrimSpace(tc.Routing.Source) == "" {
			tc.Routing.Source = intakesvc.SourceAgent
		}
		return
	}
	if !cfg.PlannerEnabled || !cfg.PlannerOnCreativeRequest {
		return
	}
	trigger := detectPlannerTrigger(input, tc, cfg)
	if trigger == "" {
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
	tc.Planner.Trigger = trigger
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

func detectPlannerTrigger(input *QueryInput, tc *intakesvc.Context, cfg *agentmdl.Intake) string {
	if input == nil || tc == nil || cfg == nil {
		return ""
	}
	if plannerExploratoryStrategyRequested(input, tc, cfg) {
		return string(planner.TriggerExploratoryStrategy)
	}
	return ""
}

func plannerExploratoryStrategyRequested(input *QueryInput, tc *intakesvc.Context, cfg *agentmdl.Intake) bool {
	if input == nil || tc == nil || cfg == nil {
		return false
	}
	if suppressPlannerForDeterministicDirectAction(tc) {
		return false
	}
	if enabled := strings.ToLower(strings.TrimSpace(tc.Scope.Values["use_exploratory_strategy"])); enabled == "true" || enabled == "1" || enabled == "yes" {
		return true
	}
	if approach := strings.ToLower(strings.TrimSpace(tc.Scope.Values["approach"])); approach == "exploratory" {
		return true
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	if query == "" {
		return false
	}
	for _, phrase := range cfg.PlannerTriggerPhrases {
		phrase = strings.ToLower(strings.TrimSpace(phrase))
		if phrase == "" {
			continue
		}
		if strings.Contains(query, phrase) {
			return true
		}
	}
	explicitPhrases := []string{
		"use exploratory strategy",
		"exploratory strategy",
		"exploratory approach",
		"exploratory workflow",
		"multi-angle approach",
		"use planner",
	}
	for _, phrase := range explicitPhrases {
		if strings.Contains(query, phrase) {
			return true
		}
	}
	return false
}

func suppressPlannerForDeterministicDirectAction(tc *intakesvc.Context) bool {
	if tc == nil {
		return false
	}
	toolName := strings.TrimSpace(tc.DirectAction.ToolName)
	if toolName == "" {
		return false
	}
	return len(tc.DirectAction.Input) > 0 || strings.TrimSpace(tc.DirectAction.InputJSON) != ""
}
