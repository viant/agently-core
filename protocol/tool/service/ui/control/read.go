package control

import (
	"context"
	"encoding/json"
	"fmt"
	identity "github.com/viant/agently-core/protocol/resource"
	svc "github.com/viant/agently-core/protocol/tool/service"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/forge/backend/uicontext"
	"github.com/viant/forge/backend/uimetadata"
	"strings"
	"time"
)

// ReadTarget constrains observation to an already-admitted current-chat window.
// Runtime pins are optional constraints, never caller-supplied authority.
type ReadTarget struct {
	Target           *uimetadata.Target    `json:"target,omitempty"`
	Resource         *identity.ResourceRef `json:"resource,omitempty"`
	ExpectedRevision *uint32               `json:"expectedRevision,omitempty"`
	ConversationID   string                `json:"conversationId,omitempty"`
	ClientID         string                `json:"clientId,omitempty"`
	WindowID         string                `json:"windowId,omitempty"`
	WindowKey        string                `json:"windowKey,omitempty"`
	DataSourceRef    string                `json:"dataSourceRef,omitempty"`
}
type GetInput struct {
	ReadTarget
	ControlID string `json:"controlId"`
}
type ListInput struct {
	ReadTarget
	Offset int `json:"offset,omitempty"`
	Limit  int `json:"limit,omitempty"`
}
type ReadOutput struct {
	ClientID          string                  `json:"clientId"`
	WindowID          string                  `json:"windowId"`
	WindowKey         string                  `json:"windowKey"`
	Resource          *identity.ResourceRef   `json:"resource,omitempty"`
	OperationRevision uint32                  `json:"operationRevision"`
	ObservedAt        int64                   `json:"observedAt"`
	Controls          []uicontext.LiveControl `json:"controls"`
	Offset            int                     `json:"offset"`
	Limit             int                     `json:"limit"`
	Total             int                     `json:"total"`
	HasMore           bool                    `json:"hasMore"`
	ReadRedacted      bool                    `json:"readRedacted,omitempty"`
	ReadTruncated     bool                    `json:"readTruncated,omitempty"`
}

func (s *Service) getControl(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*GetInput)
	if !ok || strings.TrimSpace(input.ControlID) == "" {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ReadOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	if err := s.readControls(ctx, input.ReadTarget, strings.TrimSpace(input.ControlID), 0, 2, output); err != nil {
		return err
	}
	if len(output.Controls) != 1 || output.Total != 1 {
		return fmt.Errorf("control read requires one unambiguous mounted control")
	}
	return nil
}
func (s *Service) listControls(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ListInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ReadOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	return s.readControls(ctx, input.ReadTarget, "", input.Offset, input.Limit, output)
}
func (s *Service) readControls(ctx context.Context, input ReadTarget, controlID string, offset, limit int, output *ReadOutput) error {
	if s.bridge == nil {
		return fmt.Errorf("UI bridge unavailable")
	}
	conversationID := strings.TrimSpace(requestctx.ConversationIDFromContext(ctx))
	if conversationID == "" || input.ConversationID != "" && input.ConversationID != conversationID {
		return fmt.Errorf("current conversation scope is required")
	}
	if strings.TrimSpace(input.WindowID) == "" && strings.TrimSpace(input.WindowKey) == "" {
		return fmt.Errorf("windowId or windowKey is required")
	}
	if offset < 0 || offset > 10000 {
		return fmt.Errorf("invalid control read offset")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	client := normalizeOptionalClientID(input.ClientID)
	if client == "" {
		client = normalizeOptionalClientID(requestctx.PreferredUIClientIDFromContext(ctx))
	}
	clientID, namespace, _, win, err := s.reg.FindWindow(ctx, conversationID, client, strings.TrimSpace(input.WindowID), strings.TrimSpace(input.WindowKey))
	if err != nil {
		return err
	}
	if input.Resource != nil && (win.Resource == nil || input.Resource.URI != win.Resource.URI || input.Resource.Revision != "" && input.Resource.Revision != win.Resource.Selector()) {
		return identity.ErrResourceDenied
	}
	ctx, err = s.reg.WindowRequestContext(ctx, namespace, clientID, win, nil, input.Target.WindowTarget())
	if err != nil {
		return err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != win.OperationRevision {
		return fmt.Errorf("UI state changed; refresh current control Read")
	}
	response, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{ClientID: clientID, Namespace: namespace, Method: "ui.controls.list", Params: map[string]interface{}{"windowId": win.WindowID, "conversationId": conversationID, "dataSourceRef": strings.TrimSpace(input.DataSourceRef), "controlId": controlID, "offset": offset, "limit": limit, "expectedRevision": input.ExpectedRevision, "resourceTarget": win.ResourceTarget}})
	if err != nil {
		return err
	}
	if response == nil || !response.OK {
		return fmt.Errorf("live UI control Read unavailable")
	}
	projected, err := uicontext.RedactReadJSON(response.Result, uicontext.ReadOptions{Metadata: win.Metadata, DataSourceRef: strings.TrimSpace(input.DataSourceRef), MaxItems: 50})
	if err != nil {
		return err
	}
	var page struct {
		Controls          []uicontext.LiveControl `json:"controls"`
		OperationRevision uint32                  `json:"operationRevision"`
		Total             int                     `json:"total"`
		Offset            int                     `json:"offset"`
		Limit             int                     `json:"limit"`
		HasMore           bool                    `json:"hasMore"`
		Redacted          bool                    `json:"redacted"`
		Truncated         bool                    `json:"truncated"`
	}
	if err = json.Unmarshal(projected.JSON, &page); err != nil {
		return err
	}
	if len(page.Controls) > limit || page.Offset != offset || page.Limit <= 0 || page.Limit > limit || page.Total < 0 {
		return fmt.Errorf("invalid bounded control Read response")
	}
	if page.Controls == nil {
		page.Controls = []uicontext.LiveControl{}
	}
	for _, control := range page.Controls {
		if control.ControlID == "" || controlID != "" && control.ControlID != controlID || input.DataSourceRef != "" && control.DataSourceRef != input.DataSourceRef {
			return fmt.Errorf("control Read response identity mismatch")
		}
	}
	var publicResource *identity.ResourceRef
	if win.Resource != nil {
		publicResource = &identity.ResourceRef{URI: win.Resource.URI, Revision: win.Resource.Selector()}
	}
	*output = ReadOutput{ClientID: clientID, WindowID: win.WindowID, WindowKey: win.WindowKey, Resource: publicResource, OperationRevision: page.OperationRevision, ObservedAt: time.Now().UnixMilli(), Controls: page.Controls, Offset: page.Offset, Limit: page.Limit, Total: page.Total, HasMore: page.HasMore, ReadRedacted: projected.Redacted || page.Redacted, ReadTruncated: projected.Truncated || page.Truncated}
	return nil
}
