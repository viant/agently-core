package modelcall

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/internal/textutil"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// finishModelCall persists final model call updates, including response payloads and usage.
func (o *recorderObserver) finishModelCall(ctx context.Context, msgID, status string, info Info, streamTxt string) error {
	hasResp := info.LLMResponse != nil
	debugf("finishModelCall start msg=%q status=%q has_llm_response=%v provider_resp_bytes=%d stream_bytes=%d", strings.TrimSpace(msgID), strings.TrimSpace(status), hasResp, len(info.ResponseJSON), len(streamTxt))
	if DebugEnabled() && info.LLMResponse != nil {
		for idx, choice := range info.LLMResponse.Choices {
			debugf("finishModelCall choice[%d] finish_reason=%q tool_calls=%d content_head=%q", idx, strings.TrimSpace(choice.FinishReason), len(choice.Message.ToolCalls), textutil.RuneTruncate(strings.TrimSpace(messageText(choice.Message)), 200))
		}
	}
	upd := apiconv.NewModelCall()
	upd.SetMessageID(msgID)
	upd.SetStatus(status)
	if provider := strings.TrimSpace(o.start.Provider); provider != "" {
		upd.SetProvider(provider)
	}
	if model := strings.TrimSpace(o.start.Model); model != "" {
		upd.SetModel(model)
	}
	if modelKind := strings.TrimSpace(o.start.ModelKind); modelKind != "" {
		upd.SetModelKind(modelKind)
	}
	if strings.TrimSpace(info.Err) != "" {
		upd.SetErrorMessage(info.Err)
	}
	if strings.TrimSpace(info.ErrorCode) != "" {
		upd.SetErrorCode(info.ErrorCode)
	}
	if !info.CompletedAt.IsZero() {
		upd.SetCompletedAt(info.CompletedAt)
	}

	if info.LLMResponse != nil {
		if rb, mErr := json.Marshal(info.LLMResponse); mErr == nil {
			respID, err := o.upsertInlinePayload(ctx, "", "model_response", "application/json", rb)
			if err != nil {
				errorf("finishModelCall response payload error msg=%q err=%v", strings.TrimSpace(msgID), err)
				return err
			}
			upd.SetResponsePayloadID(respID)
		}
		if strings.TrimSpace(info.LLMResponse.ResponseID) != "" {
			upd.SetTraceID(strings.TrimSpace(info.LLMResponse.ResponseID))
			if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
				runtimerequestctx.SetTurnTrace(turn.TurnID, strings.TrimSpace(info.LLMResponse.ResponseID))
			}
			if debugtrace.Enabled() {
				debugtrace.Write("modelcall", "finish_model_call", map[string]any{
					"messageID":    strings.TrimSpace(msgID),
					"status":       strings.TrimSpace(status),
					"responseID":   strings.TrimSpace(info.LLMResponse.ResponseID),
					"finishReason": strings.TrimSpace(info.FinishReason),
				})
			}
		}
	}
	if len(info.ResponseJSON) > 0 {
		provID, err := o.upsertInlinePayload(ctx, "", "provider_response", "application/json", []byte(info.ResponseJSON))
		if err != nil {
			errorf("finishModelCall provider response payload error msg=%q err=%v", strings.TrimSpace(msgID), err)
			return err
		}
		upd.SetProviderResponsePayloadID(provID)
		_ = debugtrace.WritePayload("llm-provider-response", msgID, []byte(info.ResponseJSON))
	}
	if strings.TrimSpace(streamTxt) != "" {
		sid := strings.TrimSpace(o.streamPayloadID)
		if sid == "" {
			sid = uuid.New().String()
		}
		if _, err := o.upsertInlinePayload(ctx, sid, "model_stream", "text/plain", []byte(streamTxt)); err != nil {
			errorf("finishModelCall stream payload error msg=%q err=%v", strings.TrimSpace(msgID), err)
			return err
		}
		upd.SetStreamPayloadID(sid)
	}
	if info.Usage != nil {
		u := info.Usage
		if u.PromptTokens > 0 {
			upd.SetPromptTokens(u.PromptTokens)
		}
		if u.CompletionTokens > 0 {
			upd.SetCompletionTokens(u.CompletionTokens)
		}
		if u.TotalTokens > 0 {
			upd.SetTotalTokens(u.TotalTokens)
		}
		if u.CachedTokens > 0 {
			upd.SetPromptCachedTokens(u.CachedTokens)
		}
		if o.priceProvider != nil {
			inP, outP, cachedP, ok := o.priceProvider.TokenPrices(strings.TrimSpace(info.Model))
			if !ok {
				debugPricingf("no prices found for model=%s", strings.TrimSpace(info.Model))
			}
			if ok {
				cached := u.CachedTokens
				if cached == 0 && u.PromptCachedTokens > 0 {
					cached = u.PromptCachedTokens
				}
				cost := (float64(u.PromptTokens)*inP + float64(u.CompletionTokens)*outP + float64(cached)*cachedP) / 1000.0
				if cost > 0 {
					upd.SetCost(cost)
					debugPricingf("computed cost model=%s in=%.6f out=%.6f cached=%.6f -> cost=%.6f", strings.TrimSpace(info.Model), inP, outP, cachedP, cost)
				} else {
					debugPricingf("computed zero/negative cost model=%s in=%.6f out=%.6f cached=%.6f", strings.TrimSpace(info.Model), inP, outP, cachedP)
				}
			}
		} else {
			debugPricingf("price provider not set; skipping cost for model=%s", strings.TrimSpace(info.Model))
		}
	}
	patchCtx := ctx
	if info.LLMResponse != nil && len(info.LLMResponse.Choices) > 0 {
		content, hasToolCalls := AssistantContentFromResponse(info.LLMResponse)
		content = strings.TrimSpace(content)
		preamble := strings.TrimSpace(AssistantPreambleFromResponse(info.LLMResponse, content))
		finishReason := strings.TrimSpace(info.FinishReason)
		if finishReason == "" && len(info.LLMResponse.Choices) > 0 {
			finishReason = strings.TrimSpace(info.LLMResponse.Choices[0].FinishReason)
		}
		finishLower := strings.ToLower(finishReason)
		isToolRelated := hasToolCalls || strings.Contains(finishLower, "tool")
		isFinalStop := finishLower == "stop" || finishLower == "end_turn" || finishLower == "length"
		finalResponse := isFinalStop && !isToolRelated && content != ""
		patchCtx = runtimerequestctx.WithModelCompletionMeta(ctx, runtimerequestctx.ModelCompletionMeta{
			Content:       content,
			Preamble:      preamble,
			FinalResponse: finalResponse,
			FinishReason:  finishReason,
		})
	}
	if err := o.client.PatchModelCall(patchCtx, upd); err != nil {
		errorf("finishModelCall patch model call error msg=%q err=%v", strings.TrimSpace(msgID), err)
		return err
	}
	debugf("finishModelCall ok msg=%q status=%q", strings.TrimSpace(msgID), strings.TrimSpace(status))
	return nil
}

var markdownLinkPattern = regexp.MustCompile(`\[(.*?)\]\((.*?)\)`)

func normalizeComparableText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = markdownLinkPattern.ReplaceAllString(value, "$1")
	value = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsSpace(r):
			return r
		default:
			return ' '
		}
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func (o *recorderObserver) isLikelyUserEcho(ctx context.Context, assistantContent string) bool {
	assistantText := normalizeComparableText(assistantContent)
	if assistantText == "" {
		return false
	}
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if !ok {
		return false
	}
	for _, candidateID := range []string{strings.TrimSpace(turn.ParentMessageID), strings.TrimSpace(turn.TurnID)} {
		if candidateID == "" {
			continue
		}
		msg, err := o.client.GetMessage(ctx, candidateID)
		if err != nil || msg == nil || !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			continue
		}
		userText := normalizeComparableText(valueOrEmptyPtr(msg.RawContent))
		if userText == "" {
			userText = normalizeComparableText(valueOrEmptyPtr(msg.Content))
		}
		if assistantText == userText && userText != "" {
			return true
		}
	}
	return false
}

func valueOrEmptyPtr(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

// --- transient debug helpers (enabled with AGENTLY_DEBUG_PRICING=1) ---
func debugPricingEnabled() bool { return logx.EnabledFor("AGENTLY_DEBUG_PRICING") }
func debugPricingf(format string, args ...interface{}) {
	if !debugPricingEnabled() {
		return
	}
	logx.Debugf("pricing", format, args...)
}
