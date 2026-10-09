package workspace

import (
	"context"
	"fmt"
	"net/http"

	iauth "github.com/viant/agently-core/internal/auth"
	ws "github.com/viant/agently-core/workspace"
	wscfg "github.com/viant/agently-core/workspace/config"
)

// MetadataError retains transport status and safe message for every adapter.
type MetadataError struct {
	Status  int
	Message string
	Cause   error
}

func (e *MetadataError) Error() string { return e.Message }
func (e *MetadataError) Unwrap() error { return e.Cause }
func metadataFailure(status int, message string, cause error) error {
	return &MetadataError{Status: status, Message: message, Cause: cause}
}

func (h *MetadataHandler) Metadata(ctx context.Context) (*MetadataResponse, error) {
	resp := MetadataResponse{
		WorkspaceRoot:    ws.Root(),
		WorkspaceVersion: resolveWorkspaceVersion(ws.Root()),
		Version:          h.version,
		Capabilities: Capabilities{
			WorkspaceLayout:       true,
			AgentAutoSelection:    true,
			ModelAutoSelection:    false,
			ToolAutoSelection:     h.defaults != nil && h.defaults.ToolAutoSelection.Enabled,
			CompactConversation:   true,
			PruneConversation:     true,
			AnonymousSession:      true,
			MessageCursor:         true,
			StructuredElicitation: true,
			TurnStartedEvent:      true,
		},
	}
	if h.browserMCP != nil && iauth.EffectiveUserID(ctx) != "" {
		var err error
		resp.BrowserMCP, err = h.browserMCP(ctx)
		if err != nil {
			return nil, metadataFailure(http.StatusServiceUnavailable, "browser MCP configuration unavailable", err)
		}
	}
	if h.defaults != nil {
		resp.DefaultAgent = h.defaults.Agent
		resp.DefaultModel = h.defaults.Model
		resp.DefaultEmbedder = h.defaults.Embedder
		resp.AppName = h.defaults.AppName
		resp.AppIconRef = h.defaults.AppIconRef
		resp.Defaults = &Defaults{
			AppName:               h.defaults.AppName,
			AppIconRef:            h.defaults.AppIconRef,
			Agent:                 h.defaults.Agent,
			Model:                 h.defaults.Model,
			Embedder:              h.defaults.Embedder,
			AutoSelectTools:       h.defaults.ToolAutoSelection.Enabled,
			ElicitationTimeoutSec: h.defaults.ElicitationTimeoutSec,
		}
		resp.Capabilities.Reporting = h.defaults.Reporting.Enabled
	}
	if h.reportingOverride != nil {
		resp.Capabilities.Reporting = *h.reportingOverride
	}
	if h.store != nil {
		if agents, err := h.store.List(ctx, ws.KindAgent); err == nil {
			resp.AgentInfos = h.loadAgentInfos(ctx, agents)
			resp.AgentInfos, err = h.filterStarterPrompts(ctx, resp.AgentInfos)
			if err != nil {
				return nil, metadataFailure(http.StatusServiceUnavailable, "starter prompt authorization unavailable", err)
			}
			resp.Agents = agentInfoIDs(resp.AgentInfos)
		}
		if models, err := h.store.List(ctx, ws.KindModel); err == nil {
			resp.ModelInfos = h.loadModelInfos(ctx, models)
			resp.Models = modelInfoIDs(resp.ModelInfos)
		}
	}
	var composerConfig *wscfg.Root
	if loaded, err := wscfg.Load(ws.Root()); err == nil {
		composerConfig = loaded
	}
	resp.Composer = composerConfig.Composer()
	resp.Capabilities.Goals = goalsCapabilityEnabled(resp.AgentInfos)
	styles := h.styles.Current(ctx)
	resp.WorkspaceID = styles.WorkspaceID
	resp.UIStyles = styles.Styles
	resp.UIThemes = styles.Themes
	resp.UIStyleDiagnostics = styles.Diagnostics
	resp.MetadataVersion = resolveMetadataVersion(resp)
	return &resp, nil
}

func (h *MetadataHandler) PublicAgents(ctx context.Context) (*PublicAgentsResponse, error) {
	result := PublicAgentsResponse{AgentInfos: []AgentInfo{}}
	if h.store != nil {
		if agents, err := h.store.List(ctx, ws.KindAgent); err == nil {
			infos, err := h.filterStarterPrompts(ctx, h.loadAgentInfos(ctx, agents))
			if err != nil {
				return nil, metadataFailure(http.StatusServiceUnavailable, "starter prompt authorization unavailable", err)
			}
			for _, agent := range infos {
				if !agent.Internal {
					result.AgentInfos = append(result.AgentInfos, agent)
				}
			}
		}
	}
	return &result, nil
}

func writeMetadataError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if failure, ok := err.(*MetadataError); ok {
		status = failure.Status
	}
	http.Error(w, fmt.Sprint(err), status)
}
