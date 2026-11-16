package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
func (s *Service) openReportResource(ctx context.Context, in *UICommandInput) (output *UICommandOutput, resultErr error) {
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
	frameConversation := conversation
	slot := windowPinKey{Namespace: ns, ClientID: clientID}
	slot.WindowID, _ = params["windowId"].(string)
	slot.WindowID = strings.TrimSpace(slot.WindowID)
	if slot.WindowID != "" {
		params["windowId"] = slot.WindowID
	}
	_, replacing := params["expectedRevision"]
	if slot.WindowID != "" {
		// Replacing content must retain the live frame's conversation ownership.
		// The request conversation authorizes this operation; it does not adopt
		// an independent application frame into conversation cleanup/history.
		found := false
		for _, rawWindow := range snapshot.Windows {
			var frame struct {
				WindowID       string `json:"windowId"`
				ConversationID string `json:"conversationId"`
			}
			if err := json.Unmarshal(rawWindow, &frame); err != nil {
				return nil, identity.ErrResourceStale
			}
			if frame.WindowID == slot.WindowID {
				if frame.ConversationID != "" && frame.ConversationID != conversation {
					return nil, identity.ErrResourceDenied
				}
				frameConversation = frame.ConversationID
				found = true
				break
			}
		}
		if !found && replacing {
			return nil, identity.ErrResourceStale
		}
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
	options["conversationId"] = frameConversation
	options["resourceRef"] = &identity.ResourceRef{URI: key, Revision: pin.Selector()}
	delete(options, "inlineMetadata")
	delete(options, "resource")
	delete(options, "target")
	command := map[string]any{"windowKey": key, "options": options}
	for _, name := range []string{"windowId", "windowTitle", "parameters", "inTab", "expectedRevision"} {
		if value, exists := params[name]; exists {
			command[name] = value
		}
	}
	var presentation *reportPresentation
	if held, exists := s.getWindowPin(slot); exists && !sameReportCandidate(held.Resource, pin) && !replacing {
		return nil, identity.ErrResourceStale
	}
	if replacing {
		if strings.TrimSpace(slot.WindowID) == "" {
			return nil, identity.ErrResourceDenied
		}
		var owner bool
		presentation, owner, err = s.beginReportPresentation(bounded, slot, windowResourcePin{WindowKey: key, Resource: pin}, identity.ContentFingerprint(raw))
		if err != nil {
			return nil, err
		}
		if !owner {
			select {
			case <-presentation.done:
				return presentation.output, presentation.err
			case <-bounded.Done():
				return nil, bounded.Err()
			}
		}
		if presentation != nil {
			defer func() {
				resultErr = s.finishReportPresentation(slot, presentation, output, resultErr)
			}()
		} else if held, exists := s.getWindowPin(slot); exists && !sameReportCandidate(held.Resource, pin) {
			// A legacy bridge can return no presentation fence. Its requested
			// revision is not sufficient to mutate an already-bound slot.
			return nil, identity.ErrResourceStale
		}
	}
	response, err := s.hub.Call(bounded, ns, clientID, in.Method, command)
	if err != nil {
		return nil, err
	}
	if response.OK {
		// A browser acknowledgement can precede native readback or activation.
		// Preserve both the acknowledged phase and the underlying failure.
		defer func() {
			if resultErr != nil {
				resultErr = errors.Join(ErrReportOpenUnconfirmed, resultErr)
			}
		}()
	}
	fresh, err := s.ReportDefinition(bounded, &ReportDefinitionInput{Resource: identity.ResourceRef{URI: key, Revision: pin.Selector()}, ResolvedResource: &pin})
	if err != nil {
		return nil, fmt.Errorf("report presentation phase=readback: %w", err)
	}
	if fresh == nil || fresh.Resource == nil || !sameReportPin(pin, *fresh.Resource) {
		return nil, identity.ErrResourceDenied
	}
	if response.OK {
		var ack struct {
			WindowID string `json:"windowId"`
		}
		if json.Unmarshal(response.Result, &ack) != nil || ack.WindowID == "" {
			return nil, errors.New("UI report identity is unavailable")
		}
		if presentation != nil {
			if ack.WindowID != slot.WindowID {
				return nil, identity.ErrResourceStale
			}
			err = s.commitReportPresentation(slot, presentation, *fresh.Resource)
		} else {
			commitKey := windowPinKey{Namespace: ns, ClientID: clientID, WindowID: ack.WindowID}
			s.resourceMu.Lock()
			current, exists := s.resourcePins[commitKey]
			if exists && !sameReportCandidate(current.Resource, pin) {
				err = identity.ErrResourceStale
			} else {
				err = s.rememberWindowPinLocked(bounded, commitKey, windowResourcePin{WindowKey: key, Resource: *fresh.Resource})
			}
			s.resourceMu.Unlock()
		}
		if err != nil {
			return nil, err
		}
	}
	if presentation != nil && !response.OK {
		failure := errors.New(response.Error)
		if failure.Error() == "" {
			failure = errors.New("UI rejected the report presentation")
		}
		return nil, failure
	}
	return &UICommandOutput{ClientID: clientID, ID: response.ID, OK: response.OK, Error: response.Error, Result: response.Result}, nil
}

func sameReportPin(expected, fresh identity.ResolvedResource) bool {
	return expected.URI == fresh.URI && expected.ProviderIdentity == fresh.ProviderIdentity && expected.ResourceCandidate == fresh.ResourceCandidate && expected.AuthorityBinding == fresh.AuthorityBinding && !identity.MetadataExpiryExtends(fresh.ValidUntil, expected.ValidUntil) && identity.MetadataExpiryValid(fresh.ValidUntil, time.Now())
}
