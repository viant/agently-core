package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
)

func reportResourceCommand(in *UICommandInput) bool {
	if in == nil || in.Method != "ui.window.open" {
		return false
	}
	key, err := commandWindowKey(in.Params)
	return err == nil && strings.HasPrefix(key, "report://")
}

// openReportResource reuses report-catalog admission and the normal UI command.
// Reports render through the host's existing report resource renderer; they are
// not window definitions and never enter the window-designer loader.
func (s *Service) openReportResource(ctx context.Context, in *UICommandInput) (*UICommandOutput, error) {
	timeout := 15 * time.Second
	if in.TimeoutMs > 0 {
		timeout = time.Duration(in.TimeoutMs) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var params map[string]any
	raw, err := json.Marshal(in.Params)
	if err != nil || decodeSelectionJSON(raw, &params) != nil {
		return nil, identity.ErrResource
	}
	key, _ := params["windowKey"].(string)
	uri, err := identity.ParseResourceURI(key)
	if err != nil || uri.Kind != "report" {
		return nil, identity.ErrResource
	}
	var request struct {
		Resource         *identity.ResourceRef      `json:"resource"`
		ResolvedResource *identity.ResolvedResource `json:"resolvedResource"`
	}
	if json.Unmarshal(raw, &request) != nil {
		return nil, identity.ErrResource
	}
	ref := identity.ResourceRef{URI: key}
	if request.Resource != nil {
		ref = *request.Resource
	}
	if ref.URI != key {
		return nil, identity.ErrResourceDenied
	}
	conversation := strings.TrimSpace(requestctx.ConversationIDFromContext(ctx))
	if conversation == "" {
		return nil, errors.New("conversation id is required")
	}
	options, _ := params["options"].(map[string]any)
	if options == nil {
		options = map[string]any{}
	}
	if declared, _ := options["conversationId"].(string); declared != "" && declared != conversation {
		return nil, identity.ErrResourceDenied
	}
	ns, err := s.ns.Namespace(ctx)
	if err != nil || in.Namespace != "" && in.Namespace != ns {
		return nil, identity.ErrResourceDenied
	}
	if s.ns.Resolver != nil {
		owner, err := s.ns.Identity(ctx)
		if err != nil {
			return nil, err
		}
		if !owner.ValidUntil.IsZero() {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, owner.ValidUntil)
			defer cancel()
		}
	}
	clientID := strings.TrimSpace(in.ClientID)
	if clientID == "" {
		clientID, _ = s.hub.DefaultClient(ns)
	}
	snapshot, err := decodeWindowEnvelope(s.hub.Snapshot(ns, clientID))
	if err != nil || !containsClient(s.hub.ListClients(ns), clientID) || snapshot.ConversationID != conversation {
		return nil, identity.ErrResourceDenied
	}
	selected, err := s.ReportDefinition(ctx, &ReportDefinitionInput{Resource: ref, ResolvedResource: request.ResolvedResource})
	if err != nil {
		return nil, err
	}
	if selected == nil || selected.Resource == nil || selected.Resource.URI != key {
		return nil, identity.ErrResourceDenied
	}
	pin := *selected.Resource
	bounded, stop := identity.WithMetadataExpiry(ctx, pin.ValidUntil)
	defer stop()
	options["conversationId"] = conversation
	options["resourceRef"] = &identity.ResourceRef{URI: key, Revision: pin.Selector()}
	delete(options, "inlineMetadata")
	delete(options, "resource")
	delete(options, "target")
	command := map[string]any{"windowKey": key, "options": options}
	for _, name := range []string{"windowId", "windowTitle", "parameters"} {
		if value, exists := params[name]; exists {
			command[name] = value
		}
	}
	response, err := s.hub.Call(bounded, ns, clientID, in.Method, command)
	if err != nil {
		return nil, err
	}
	fresh, err := s.ReportDefinition(bounded, &ReportDefinitionInput{Resource: identity.ResourceRef{URI: key, Revision: pin.Selector()}, ResolvedResource: &pin})
	if err != nil || fresh == nil || fresh.Resource == nil || !sameReportPin(pin, *fresh.Resource) {
		return nil, identity.ErrResourceDenied
	}
	if response.OK {
		var ack struct {
			WindowID string `json:"windowId"`
		}
		if json.Unmarshal(response.Result, &ack) != nil || ack.WindowID == "" {
			return nil, errors.New("UI report identity is unavailable")
		}
		if err = s.rememberWindowPin(bounded, windowPinKey{Namespace: ns, ClientID: clientID, WindowID: ack.WindowID}, windowResourcePin{WindowKey: key, Resource: pin}); err != nil {
			return nil, err
		}
	}
	return &UICommandOutput{ClientID: clientID, ID: response.ID, OK: response.OK, Error: response.Error, Result: response.Result}, nil
}

func sameReportPin(expected, fresh identity.ResolvedResource) bool {
	return expected.URI == fresh.URI && expected.ProviderIdentity == fresh.ProviderIdentity && expected.ResourceCandidate == fresh.ResourceCandidate && expected.AuthorityBinding == fresh.AuthorityBinding && !identity.MetadataExpiryExtends(fresh.ValidUntil, expected.ValidUntil) && identity.MetadataExpiryValid(fresh.ValidUntil, time.Now())
}
