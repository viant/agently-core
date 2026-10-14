package sdk

import (
	"context"
	"encoding/json"
	convstore "github.com/viant/agently-core/app/store/conversation"
	workspaceproto "github.com/viant/agently-core/protocol/ui/workspace"
	"strings"
)

// Header reads use the same authenticated native store while excluding its
// transcript/model/tool joins. Other Client implementations retain the fallback.
func (c *backendClient) getConversationHeader(ctx context.Context, id string) (*convstore.Conversation, error) {
	return c.conv.GetConversation(ctx, id, convstore.WithIncludeTranscript(false), convstore.WithIncludeModelCall(false), convstore.WithIncludeToolCall(false))
}

// Payload omission follows canonical derivation so execution roles inferred from
// stored model responses remain identical. Only the response projection changes;
// the native transcript and payload retrieval service remain untouched.
func omitCanonicalModelPayloads(state *ConversationState) {
	if state == nil {
		return
	}
	for _, turn := range state.Turns {
		if turn == nil || turn.Execution == nil {
			continue
		}
		for _, page := range turn.Execution.Pages {
			if page == nil {
				continue
			}
			for _, step := range page.ModelSteps {
				if step == nil {
					continue
				}
				step.RequestPayload = nil
				step.ResponsePayload = nil
				step.ProviderRequestPayload = nil
				step.ProviderResponsePayload = nil
				step.StreamPayload = nil
			}
		}
	}
}

func hasTranscriptToolSteps(state *ConversationState) bool {
	if state == nil {
		return false
	}
	for _, turn := range state.Turns {
		if turn == nil || turn.Execution == nil {
			continue
		}
		for _, page := range turn.Execution.Pages {
			if page != nil && len(page.ToolSteps) > 0 {
				return true
			}
		}
	}
	return false
}

// This is an output projection after canonical role/workspace/render/feed
// derivation. Originals and lazy payload references are never modified.
func (c *backendClient) omitCanonicalToolPayloads(state *ConversationState, contexts ...context.Context) {
	ctx := context.Background()
	if len(contexts) > 0 {
		ctx = contexts[0]
	}
	c.omitCanonicalToolPayloadsWithCatalog(state, ctx, true)
}

func (c *backendClient) omitCanonicalToolPayloadsWithCatalog(state *ConversationState, ctx context.Context, loadDynamicSpecs bool) {
	if !hasTranscriptToolSteps(state) {
		return
	}
	var specs []*FeedSpec
	if loadDynamicSpecs {
		specs, _ = c.feedSpecs(ctx)
	} else if c.feeds != nil {
		specs = c.feeds.Specs()
	}
	for _, turn := range state.Turns {
		if turn == nil || turn.Execution == nil {
			continue
		}
		for _, page := range turn.Execution.Pages {
			if page == nil {
				continue
			}
			for _, step := range page.ToolSteps {
				if step == nil || step.Status != "completed" || step.ResponsePayloadID == "" || c.toolPayloadNeededForRendering(step, specs) {
					continue
				}
				step.Content = ""
				step.ResponsePayload = nil
				if step.RequestPayloadID != "" {
					step.RequestPayload = nil
				}
			}
		}
	}
}
func (c *backendClient) toolPayloadNeededForRendering(step *ToolStepState, scopedSpecs ...[]*FeedSpec) bool {
	if step.UIResourceURI != "" || step.AsyncOperation != nil {
		return true
	}
	// Preserve declared MCP Apps, report runtime and workspace acknowledgement
	// contracts regardless of the producer's arbitrary tool/server name.
	var carrier struct {
		Meta struct {
			UI struct {
				ResourceURI string `json:"resourceUri"`
			} `json:"ui"`
		} `json:"_meta"`
		ReportSpec        json.RawMessage        `json:"reportSpec"`
		ReportFill        json.RawMessage        `json:"reportFill"`
		ReportPrint       json.RawMessage        `json:"reportPrint"`
		WorkspaceObject   *workspaceproto.Object `json:"workspaceObject"`
		StructuredContent json.RawMessage        `json:"structuredContent"`
	}
	body := workspaceResponseBody(step.ResponsePayload)
	if json.Unmarshal(body, &carrier) == nil {
		if len(carrier.StructuredContent) > 0 {
			_ = json.Unmarshal(carrier.StructuredContent, &carrier)
		}
		if strings.HasPrefix(carrier.Meta.UI.ResourceURI, "ui://") || carrier.WorkspaceObject != nil || len(carrier.ReportSpec) > 0 && len(carrier.ReportFill) > 0 && len(carrier.ReportPrint) > 0 {
			return true
		}
	}
	service, method := parseToolName(step.ToolName)
	// These exact registered producers carry window parameters/context used by
	// the existing workspace restoration and lazy payload hydration path.
	switch service + "/" + method {
	case "ui/view/open", "ui/window/open", "ui/window/list", "ui/window/get", "ui/window/show", "ui/window/close", "ui/window/setformdata", "ui/context/get":
		return true
	}
	var specs []*FeedSpec
	if len(scopedSpecs) > 0 {
		specs = scopedSpecs[0]
	} else if c.feeds != nil {
		specs = c.feeds.Specs()
	}
	if specs != nil {
		for _, spec := range specs {
			if spec != nil && matchesRule(spec.Match, service, method) {
				return true
			}
		}
	}
	return false
}
