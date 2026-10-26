package window

import (
	"context"
	"encoding/json"
	"fmt"
	identity "github.com/viant/agently-core/protocol/resource"
	svc "github.com/viant/agently-core/protocol/tool/service"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/forge/backend/uimetadata"
	"strings"
)

// RefreshInput names only declared UI scope; no endpoint, handler or datasource
// can be supplied by the caller. The host rechecks the held current window pin.
type RefreshInput struct {
	ClientID         string                `json:"clientId,omitempty"`
	WindowID         string                `json:"windowId"`
	Resource         *identity.ResourceRef `json:"resource,omitempty"`
	Target           *uimetadata.Target    `json:"target,omitempty"`
	ExpectedRevision *uint32               `json:"expectedRevision,omitempty"`
	Scope            string                `json:"scope,omitempty"`
	ContainerID      string                `json:"containerId,omitempty"`
	TabID            string                `json:"tabId,omitempty"`
	FormID           string                `json:"formId,omitempty"`
	ReloadMetadata   bool                  `json:"reloadMetadata,omitempty"`
}
type RefreshCapability struct {
	WindowID            string `json:"windowId"`
	Scope               string `json:"scope"`
	ContainerID         string `json:"containerId,omitempty"`
	TabID               string `json:"tabId,omitempty"`
	FormID              string `json:"formId,omitempty"`
	Metadata            bool   `json:"metadata"`
	Data                bool   `json:"data"`
	PreservesLocalState bool   `json:"preservesLocalState"`
}
type RefreshOutput struct {
	OK                bool                `json:"ok"`
	ClientID          string              `json:"clientId"`
	Status            string              `json:"status,omitempty"`
	Scope             string              `json:"scope,omitempty"`
	MetadataRefreshed bool                `json:"metadataRefreshed,omitempty"`
	DataRefreshed     bool                `json:"dataRefreshed,omitempty"`
	OperationRevision uint32              `json:"operationRevision"`
	Capabilities      []RefreshCapability `json:"capabilities,omitempty"`
}

func (s *Service) refresh(ctx context.Context, in, out interface{}) error {
	return s.workspaceRefresh(ctx, in, out, false)
}
func (s *Service) refreshCapabilities(ctx context.Context, in, out interface{}) error {
	return s.workspaceRefresh(ctx, in, out, true)
}
func (s *Service) workspaceRefresh(ctx context.Context, in, out interface{}, list bool) error {
	input, ok := in.(*RefreshInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*RefreshOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	if s.bridge == nil {
		return fmt.Errorf("UI bridge not configured")
	}
	windowID := strings.TrimSpace(input.WindowID)
	if windowID == "" {
		return fmt.Errorf("windowId is required")
	}
	scope := strings.TrimSpace(input.Scope)
	if scope == "" {
		scope = "window"
	}
	if scope != "window" && scope != "tab" && scope != "form" {
		return fmt.Errorf("unsupported refresh scope")
	}
	if !list && (scope == "tab" && (input.ContainerID == "" || input.TabID == "") || scope == "form" && input.FormID == "") {
		return fmt.Errorf("refresh scope requires its declared target identifiers")
	}
	target, err := s.resolveWindowTarget(ctx, input.ClientID, windowID, "")
	if err != nil {
		return err
	}
	if input.Resource != nil && (target.Window.Resource == nil || input.Resource.URI != target.Window.Resource.URI || input.Resource.Revision != "" && input.Resource.Revision != target.Window.Resource.Selector()) {
		return fmt.Errorf("refresh resource does not match current window")
	}
	ctx, err = s.reg.WindowRequestContext(ctx, target.Namespace, target.ClientID, target.Window, nil, input.Target.WindowTarget())
	if err != nil {
		return err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != target.Window.OperationRevision {
		return fmt.Errorf("stale UI operation revision; read current UI before refreshing")
	}
	method := "ui.workspace.refresh"
	if list {
		method = "ui.workspace.refreshCapabilities"
	}
	params := map[string]interface{}{"windowId": windowID, "conversationId": target.Window.ConversationID, "resourceTarget": target.Window.ResourceTarget, "expectedRevision": input.ExpectedRevision, "scope": scope, "reloadMetadata": input.ReloadMetadata}
	if input.ContainerID != "" {
		params["containerId"] = input.ContainerID
	}
	if input.TabID != "" {
		params["tabId"] = input.TabID
	}
	if input.FormID != "" {
		params["formId"] = input.FormID
	}
	response, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{ClientID: target.ClientID, Namespace: target.Namespace, Method: method, Params: params})
	if err != nil {
		return err
	}
	if response == nil || !response.OK {
		return fmt.Errorf("UI refresh rejected")
	}
	var result RefreshOutput
	if err = json.Unmarshal(response.Result, &result); err != nil {
		return err
	}
	if !list && (result.Status != "refreshed" || result.Scope != scope || result.MetadataRefreshed != input.ReloadMetadata || result.DataRefreshed == input.ReloadMetadata) {
		return fmt.Errorf("UI refresh result does not verify requested scope and mode")
	}
	for _, capability := range result.Capabilities {
		if capability.WindowID != windowID {
			return fmt.Errorf("refresh capability is outside requested window")
		}
	}
	result.ClientID = target.ClientID
	result.OK = true
	*output = result
	return nil
}
