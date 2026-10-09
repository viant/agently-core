package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/viant/agently-core/runtime/requestctx"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
)

type windowPinKey struct{ Namespace, ClientID, WindowID string }
type windowResourcePin struct {
	WindowKey string
	Resource  identity.ResolvedResource
	Target    *types.WindowTarget
}

func (s *Service) canonicalWindowCatalog() bool {
	if s == nil || s.cfg == nil {
		return false
	}
	catalog, ok := s.cfg.WindowDefinitions.(interface{ UsesResourceResolution() bool })
	return ok && catalog.UsesResourceResolution()
}
func (c *MetadataWindowCatalog) RevalidateResource(ctx context.Context, windowKey string, pin identity.ResolvedResource) (*identity.ResolvedResource, error) {
	if c == nil || c.resolveResource == nil {
		return nil, identity.ErrResourceDenied
	}
	canonical, err := c.canonicalWindowKey(ctx, windowKey)
	if err != nil {
		return nil, identity.ErrResourceDenied
	}
	windowKey = canonical
	for _, entry := range c.entries {
		if entry.WindowID == windowKey {
			allowed, err := c.checkEntryAccess(ctx, entry)
			if err != nil || !allowed {
				return nil, identity.ErrResourceDenied
			}
			resolver, ref, err := c.resolveResource(ctx, windowKey)
			if err != nil || resolver == nil || ref.URI != pin.URI {
				return nil, identity.ErrResourceDenied
			}
			_, fresh, err := resolver.ReadResolved(ctx, pin)
			return fresh, err
		}
	}
	return nil, identity.ErrResourceDenied
}
func (s *Service) revalidateWindowPin(ctx context.Context, pin windowResourcePin) (*identity.ResolvedResource, error) {
	if catalog, ok := s.cfg.WindowDefinitions.(interface {
		RevalidateWindowResource(context.Context, string, identity.ResolvedResource, *types.WindowTarget) (*identity.ResolvedResource, error)
	}); ok {
		return catalog.RevalidateWindowResource(ctx, pin.WindowKey, pin.Resource, pin.Target)
	}
	catalog, ok := s.cfg.WindowDefinitions.(interface {
		RevalidateResource(context.Context, string, identity.ResolvedResource) (*identity.ResolvedResource, error)
	})
	if !ok {
		return nil, identity.ErrResourceDenied
	}
	return catalog.RevalidateResource(ctx, pin.WindowKey, pin.Resource)
}
func (s *Service) rememberWindowPin(ctx context.Context, key windowPinKey, pin windowResourcePin) error {
	if pin.Target != nil {
		normalized, err := pin.Target.Normalize()
		if err != nil {
			return err
		}
		pin.Target = &normalized
	}
	if _, err := s.revalidateWindowPin(ctx, pin); err != nil {
		return errors.New("window resource is unavailable")
	}
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	if s.resourcePins == nil {
		s.resourcePins = map[windowPinKey]windowResourcePin{}
	}
	for key, value := range s.resourcePins {
		if !value.Resource.ValidUntil.After(time.Now()) {
			delete(s.resourcePins, key)
		}
	}
	if len(s.resourcePins) >= 1024 {
		if _, exists := s.resourcePins[key]; !exists {
			return errors.New("window resource capacity exceeded")
		}
	}
	s.resourcePins[key] = pin
	return nil
}
func (s *Service) getWindowPin(key windowPinKey) (windowResourcePin, bool) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	pin, ok := s.resourcePins[key]
	return pin, ok
}
func (s *Service) authorizePinnedCommand(ctx context.Context, ns, clientID, method string, params any) (*windowResourcePin, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var input map[string]any
	if json.Unmarshal(raw, &input) != nil {
		return nil, identity.ErrResourceDenied
	}
	id, _ := input["windowId"].(string)
	// Commands without an exact window instance cannot act on implicit selected
	// state in canonical mode. Read-only context uses guarded snapshot endpoints.
	if id == "" {
		return nil, errors.New("resolved window instance is required")
	}
	pin, ok := s.getWindowPin(windowPinKey{Namespace: ns, ClientID: clientID, WindowID: id})
	if !ok {
		return nil, identity.ErrResourceDenied
	}
	for _, key := range []string{"target", "resourceTarget"} {
		if value, exists := input[key]; exists {
			raw, err := json.Marshal(value)
			var target *types.WindowTarget
			if err != nil || json.Unmarshal(raw, &target) != nil || !types.SameWindowTarget(target, pin.Target) {
				return nil, identity.ErrResourceDenied
			}
		}
	}
	fresh, err := s.revalidateWindowPin(ctx, pin)
	if err != nil {
		return nil, errors.New("window resource is unavailable")
	}
	if s.cfg.ResolvedWindowAuthorizer == nil || s.cfg.ResolvedWindowAuthorizer(ctx, *fresh, method, input) != nil {
		return nil, identity.ErrResourceDenied
	}
	return &pin, nil
}
func (s *Service) filterResourceSnapshot(ctx context.Context, ns, clientID string, raw json.RawMessage) (json.RawMessage, error) {
	envelope, err := decodeWindowEnvelope(raw)
	if err != nil {
		return nil, err
	}
	result := windowEnvelope{Windows: []json.RawMessage{}, ConversationID: envelope.ConversationID, ClientID: envelope.ClientID}
	for _, window := range envelope.Windows {
		item, err := decodeWindowIdentity(window)
		if err != nil {
			return nil, err
		}
		if item.WindowID == "chat/new" && item.WindowKey == "chat/new" {
			// A minimal system anchor permits a connected client to open its
			// first resource; browser metadata and data never become authority.
			result.Windows = append(result.Windows, json.RawMessage(`{"windowId":"chat/new","windowKey":"chat/new"}`))
			continue
		}
		pin, ok := s.getWindowPin(windowPinKey{Namespace: ns, ClientID: clientID, WindowID: item.WindowID})
		if !ok || item.WindowKey != pin.WindowKey {
			continue
		}
		if _, err := s.revalidateWindowPin(ctx, pin); err != nil {
			continue
		}
		definition, err := s.cfg.WindowDefinitions.Get(ctx, &WindowDefinitionGetInput{WindowID: pin.WindowKey, ResolvedResource: &pin.Resource, Target: pin.Target})
		if err != nil || definition == nil || definition.Definition == nil {
			continue
		}
		// Snapshot resource text cannot replace the trusted server pin.
		var content map[string]any
		if json.Unmarshal(window, &content) != nil {
			return nil, identity.ErrResourceDenied
		}
		content["resource"] = pin.Resource
		content["resourceTarget"] = pin.Target
		content["metadata"] = definition.Definition
		if sources, ok := content["dataSources"].(map[string]any); ok {
			for id := range sources {
				if _, allowed := definition.Definition.DataSource[id]; !allowed {
					delete(sources, id)
				}
			}
		}
		encoded, err := json.Marshal(content)
		if err != nil {
			return nil, err
		}
		result.Windows = append(result.Windows, encoded)
		if envelope.Selected.WindowID == item.WindowID {
			result.Selected.WindowID = item.WindowID
		}
	}
	// Global controls/events/datasource snapshots can refer to revoked windows.
	// Keep only the explicitly admitted instance envelope in canonical mode.
	return json.Marshal(result)
}

// UsesWindowResourceResolution reports the trusted local-window resolver mode.
func (s *Service) UsesWindowResourceResolution() bool { return s.canonicalWindowCatalog() }

// FilterResourceSnapshot lets embedding UI registries apply the same server-held
// instance authority before ingesting bridge snapshots. Legacy mode is unchanged.
func (s *Service) FilterResourceSnapshot(ctx context.Context, namespace, clientID string, raw json.RawMessage) (json.RawMessage, error) {
	if !s.canonicalWindowCatalog() {
		return append(json.RawMessage(nil), raw...), nil
	}
	return s.filterResourceSnapshot(ctx, namespace, clientID, raw)
}

// WindowResource revalidates an exact server-held instance, including archived
// event callers with no currently published browser snapshot.
func (s *Service) WindowResource(ctx context.Context, namespace, clientID, windowID, windowKey string) (*identity.ResolvedResource, error) {
	if !s.canonicalWindowCatalog() {
		return nil, identity.ErrResourceDenied
	}
	pin, ok := s.getWindowPin(windowPinKey{Namespace: namespace, ClientID: clientID, WindowID: windowID})
	if !ok || windowKey != "" && pin.WindowKey != windowKey {
		return nil, identity.ErrResourceDenied
	}
	// Only this exact pinned read shares verified facts. The local phase ends
	// before callers persist events or execute any component/datasource action.
	readCtx, stop := context.WithDeadline(ctx, pin.Resource.ValidUntil)
	defer stop()
	finish := func() error { return nil }
	if !requestctx.WindowReadDecisionActive(readCtx) {
		var err error
		readCtx, finish, err = s.BeginWindowReadDecision(readCtx)
		if err != nil {
			return nil, err
		}
	}
	current, readErr := s.revalidateWindowPin(readCtx, pin)
	finalErr := finish()
	if readErr != nil {
		return nil, readErr
	}
	if finalErr != nil {
		return nil, finalErr
	}
	if readCtx.Err() != nil || !pin.Resource.ValidUntil.After(time.Now()) || current == nil || current.URI != pin.Resource.URI || current.ProviderIdentity != pin.Resource.ProviderIdentity || current.ResourceCandidate != pin.Resource.ResourceCandidate || current.AuthorityBinding != pin.Resource.AuthorityBinding || current.ValidUntil.After(pin.Resource.ValidUntil) || !current.ValidUntil.After(time.Now()) {
		return nil, identity.ErrResourceDenied
	}
	terminalLease := pin.Resource.ValidUntil
	if current.ValidUntil.Before(terminalLease) {
		terminalLease = current.ValidUntil
	}
	terminalCtx, stopTerminal := context.WithDeadline(ctx, terminalLease)
	defer stopTerminal()
	if err := s.checkWindowContentCurrent(terminalCtx, pin); err != nil {
		return nil, err
	}
	if terminalCtx.Err() != nil || !current.ValidUntil.After(time.Now()) || !pin.Resource.ValidUntil.After(time.Now()) {
		return nil, identity.ErrResourceDenied
	}
	return current, nil
}

func (s *Service) checkWindowContentCurrent(ctx context.Context, pin windowResourcePin) error {
	if ctx == nil || ctx.Err() != nil || !pin.Resource.ValidUntil.After(time.Now()) {
		return identity.ErrResourceDenied
	}
	bounded, stop := context.WithDeadline(ctx, pin.Resource.ValidUntil)
	defer stop()
	ctx = bounded
	if check, ok := s.cfg.WindowDefinitions.(interface {
		CheckWindowContent(context.Context, string, identity.ResolvedResource, *types.WindowTarget) error
	}); ok {
		if err := check.CheckWindowContent(ctx, pin.WindowKey, pin.Resource, pin.Target); err != nil {
			return err
		}
	} else {
		// Older trusted catalogs retain their original authorized pinned read. No
		// missing-hook success is allowed, and the parent remains outside our phase.
		terminal, err := s.revalidateWindowPin(ctx, pin)
		if err != nil {
			return err
		}
		if terminal == nil || terminal.URI != pin.Resource.URI || terminal.ProviderIdentity != pin.Resource.ProviderIdentity || terminal.ResourceCandidate != pin.Resource.ResourceCandidate || terminal.AuthorityBinding != pin.Resource.AuthorityBinding || terminal.ValidUntil.After(pin.Resource.ValidUntil) || !terminal.ValidUntil.After(time.Now()) {
			return identity.ErrResourceDenied
		}
	}
	if ctx.Err() != nil || !pin.Resource.ValidUntil.After(time.Now()) {
		return identity.ErrResourceDenied
	}
	return nil
}
