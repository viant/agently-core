package agent

import (
	"context"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/service/core"
)

func EnsureGenerateOptions(ctx context.Context, i *core.GenerateInput, agent *agent.Agent) {
	// Propagate agent-level temperature to per-request options if not explicitly set.
	// Keep any existing options provided via model selection.
	if i.Options == nil {
		i.Options = &llm.Options{}
	}

	if i.Options.Temperature == 0 && agent.Temperature != 0 {
		i.Options.Temperature = agent.Temperature
	}
	// Carry agent-level parallel tool-calls preference; capability gating
	// happens later in core.updateFlags based on provider/model support.
	// When the agent doesn't explicitly set it (nil), default to true so
	// models that support parallel tool calls use it by default.
	if agent.ParallelToolCalls != nil {
		i.Options.ParallelToolCalls = *agent.ParallelToolCalls
	} else {
		i.Options.ParallelToolCalls = true // default: enable parallel tool calls
	}
	// Pass attach mode as metadata so providers can honor ref vs inline.
	if i.Options.Metadata == nil {
		i.Options.Metadata = map[string]interface{}{}
	}

	// Reasoning defaults: if not explicitly set on request, inherit from agent
	if i.Options.Reasoning == nil && agent.Reasoning != nil {
		i.Options.Reasoning = agent.Reasoning
	}

	// Continuation-by-response-id is now controlled by model/provider config
	// (options.ContextContinuation). Agent-level override removed.
	mode := "ref"
	if agent.Attachment != nil {
		if m := strings.TrimSpace(strings.ToLower(agent.Attachment.Mode)); m != "" {
			mode = m
		}
		if agent.Attachment.TTLSec > 0 {
			i.Options.Metadata["attachmentTTLSec"] = agent.Attachment.TTLSec
		}

	}

	// No additional defaults here; Agent.Init sets defaults in a single place
	i.Options.Metadata["attachMode"] = mode
	// Use agentId for provider-side scoping (uploads, telemetry). Agent name is reserved for prompt identity only.
	i.Options.Metadata["agentId"] = agent.ID
	if agent.WantsModelArtifactGeneration() {
		i.Options.Metadata["modelArtifactGeneration"] = true
	}
	if strings.EqualFold(strings.TrimSpace(agent.ID), "image_generator") {
		i.Options.Metadata["forceImageGeneration"] = true
	}

	if ui := auth.User(ctx); ui != nil {
		uname := strings.TrimSpace(ui.Subject)
		if uname == "" {
			uname = strings.TrimSpace(ui.Email)
		}
		i.UserID = uname
	}

}
