package service

import (
	"context"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/forge/backend/types"
	"strings"
)

// BindAdmittedNativePresentationWindow receives only a server-selected window
// definition. Query instance ids are routing hints checked against the existing
// negotiated transport session; browser snapshot contents grant nothing.
func (s *Service) BindAdmittedNativePresentationWindow(ctx context.Context, clientID, windowID, windowKey string, admitted *types.Window) (bool, error) {
	if requestctx.UIBridgeSessionIDFromContext(ctx) == "" {
		return false, nil
	}
	if admitted == nil || admitted.Resource == nil {
		return false, nil
	}
	windowID, clientID = strings.TrimSpace(windowID), strings.TrimSpace(clientID)
	uri, uriErr := identity.ParseResourceURI(admitted.Resource.URI)
	if windowID == "" || clientID == "" || uriErr != nil || (uri.Kind != "window" && uri.Kind != "report") || (uri.Kind == "window" && admitted.ResourceTarget == nil) || (uri.Kind == "report" && windowKey != admitted.Resource.URI) {
		return true, identity.ErrResourceDenied
	}
	bridge, session, err := s.nativePresentationSession(ctx)
	if err != nil || session.clientID != clientID {
		return true, identity.ErrResourceDenied
	}
	key := windowPinKey{Namespace: session.ns, ClientID: clientID, WindowID: windowID}
	// A report loader has already reauthorized this exact candidate for the
	// current native owner. Credential rotation may replace its authority while
	// retaining the mounted instance and its observed output. Direct reads with
	// the old pin still fail at the owning report catalog.
	if old, exists := s.getWindowPin(key); exists && (old.Resource.URI != admitted.Resource.URI || old.Resource.ProviderIdentity != admitted.Resource.ProviderIdentity || old.Resource.ResourceCandidate != admitted.Resource.ResourceCandidate || (uri.Kind != "report" && old.Resource.AuthorityBinding != admitted.Resource.AuthorityBinding)) {
		return true, identity.ErrResourceDenied
	}
	if bridge.sessionInfoByID(requestctx.UIBridgeSessionIDFromContext(ctx)) != session {
		return true, identity.ErrResourceDenied
	}
	return true, s.rememberWindowPin(ctx, key, windowResourcePin{WindowKey: windowKey, Resource: *admitted.Resource, Target: admitted.ResourceTarget})
}
