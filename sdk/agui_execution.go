package sdk

import (
	"bytes"
	"encoding/json"
	"fmt"

	agentmodel "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/binding"
	agentsvc "github.com/viant/agently-core/service/agent"
)

func aguiExecutionCapabilities() map[string]bool {
	return map[string]bool{"agentId": true, "model": true, "backendTools": true, "toolBundles": true, "reasoningEffort": true, "displayQuery": true, "context": true, "attachments": true, "autoSelectTools": true, "autoSummarize": true, "disableChains": true, "allowedChains": true, "resourceURIs": true, "toolCallExposure": true, "useServerState": true}
}

func aguiUsesServerState(input *agui.RunAgentInput) bool {
	var properties agui.ForwardedProps
	return input != nil && json.Unmarshal(input.ForwardedProps, &properties) == nil && properties.Agently != nil && properties.Agently.Payload != nil && properties.Agently.Payload.UseServerState != nil && *properties.Agently.Payload.UseServerState
}

// applyAGUIExecution preserves the web's existing native query controls while
// keeping standard frontend tools separate from the backend tool allow-list.
// Identity/conversation/history never come from the browser's extension payload.
func applyAGUIExecution(query *agentsvc.QueryInput, extension *agui.Extension, resuming bool) error {
	if extension == nil || extension.Payload == nil {
		return nil
	}
	input := extension.Payload
	query.AgentID, query.ModelOverride = input.AgentID, input.Model
	if resuming && (input.BackendTools != nil || input.ToolBundles != nil || input.ReasoningEffort != nil || input.DisplayQuery != nil || len(input.Context) != 0 || input.Attachments != nil || input.AutoSelectTools != nil || input.AutoSummarize != nil || input.DisableChains != nil || input.AllowedChains != nil || input.ResourceURIs != nil || input.ToolCallExposure != nil) {
		return fmt.Errorf("execution controls belong to the original turn; resume cannot replace them")
	}
	query.ToolsAllowed = append([]string(nil), input.BackendTools...)
	query.ToolBundles = append([]string(nil), input.ToolBundles...)
	query.AllowedChains = append([]string(nil), input.AllowedChains...)
	query.ResourceURIs = append([]string(nil), input.ResourceURIs...)
	if input.AutoSelectTools != nil {
		value := *input.AutoSelectTools
		query.AutoSelectTools = &value
	}
	if input.AutoSummarize != nil {
		value := *input.AutoSummarize
		query.AutoSummarize = &value
	}
	if input.DisableChains != nil {
		query.DisableChains = *input.DisableChains
	}
	if input.ToolCallExposure != nil {
		value := agentmodel.ToolCallExposure(*input.ToolCallExposure)
		query.ToolCallExposure = &value
	}
	if input.ReasoningEffort != nil {
		value := *input.ReasoningEffort
		query.ReasoningEffort = &value
	}
	if input.DisplayQuery != nil {
		query.DisplayQuery = *input.DisplayQuery
	}
	if len(input.Context) > 0 {
		var values map[string]any
		decoder := json.NewDecoder(bytes.NewReader(input.Context))
		decoder.UseNumber()
		if err := decoder.Decode(&values); err != nil {
			return fmt.Errorf("invalid execution context: %w", err)
		}
		if query.Context == nil {
			query.Context = map[string]any{}
		}
		for key, value := range values {
			if key != "agui" {
				query.Context[key] = value
			}
		}
	}
	for _, ref := range input.Attachments {
		query.Attachments = append(query.Attachments, &binding.Attachment{Name: ref.Name, URI: ref.URI, Mime: ref.Mime, StagingFolder: ref.StagingFolder})
	}
	return nil
}
