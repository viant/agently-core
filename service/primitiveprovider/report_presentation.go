package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
)

// This state belongs to the existing instance pin, and only to one explicit
// open invocation. It is not a resource grant or a second instance registry.
type reportPresentation struct {
	ctx                    context.Context
	started                time.Time
	sessionID              string
	session                *httpSessionInfo
	old, next              windowResourcePin
	operation, requestHash string
	activated              bool // all mutable fields are protected by resourceMu
	done                   chan struct{}
	output                 *UICommandOutput
	err                    error
}

func sameReportCandidate(a, b identity.ResolvedResource) bool {
	return a.URI == b.URI && a.ProviderIdentity == b.ProviderIdentity && a.ResourceCandidate == b.ResourceCandidate
}

func (s *Service) beginReportPresentation(ctx context.Context, key windowPinKey, next windowResourcePin, requestHash string) (*reportPresentation, bool, error) {
	s.hub.mu.RLock()
	receiver := s.hub.clients[key.Namespace][key.ClientID]
	s.hub.mu.RUnlock()
	if receiver == nil {
		return nil, false, identity.ErrResourceDenied
	}
	sessionID := ""
	if receiver.ctx != nil {
		sessionID = sessionIDFromContext(receiver.ctx)
	}
	s.hub.httpMu.Lock()
	bridge := s.hub.httpBridge
	s.hub.httpMu.Unlock()
	if bridge == nil || sessionID == "" {
		// The legacy bridge has no native HTTP preflight. Existing ordinary command
		// handling remains unchanged; authenticated native hosts never downgrade.
		if s.ns.Resolver == nil {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("native report presentation session unavailable: %w", errors.ErrUnsupported)
	}
	session := bridge.sessionInfoByID(sessionID)
	if session == nil || session.receiver != receiver || session.ns != key.Namespace || session.clientID != key.ClientID {
		return nil, false, identity.ErrResourceDenied
	}
	operation := requestctx.ToolMessageIDFromContext(ctx)
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	old, ok := s.resourcePins[key]
	if !ok {
		return nil, false, identity.ErrResourceStale
	}
	if p := old.pendingReport; p != nil && p.ctx.Err() == nil && p.session == session && p.sessionID == sessionID {
		if operation != "" && p.operation == operation && p.requestHash == requestHash && sameReportPin(p.next.Resource, next.Resource) {
			return p, false, nil
		}
		return nil, false, identity.ErrResourceStale
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	old.pendingReport = nil
	p := &reportPresentation{ctx: ctx, started: time.Now(), sessionID: sessionID, session: session, old: old, next: next, operation: operation, requestHash: requestHash, done: make(chan struct{})}
	held := old
	held.pendingReport = p
	s.resourcePins[key] = held
	return p, true, nil
}

// The snapshot is only a negative consistency/close fence. Resource authority
// still comes exclusively from the freshly admitted server-selected candidate.
func (s *Service) presentationSnapshotLocked(key windowPinKey, p *reportPresentation) error {
	state := s.hub.snapshots[key.Namespace][key.ClientID]
	receiver := s.hub.clients[key.Namespace][key.ClientID]
	if receiver != p.session.receiver || state.UpdatedAt.Before(p.started) {
		return identity.ErrResourceStale
	}
	envelope, err := decodeWindowEnvelope(state.Data)
	if err != nil {
		return identity.ErrResourceStale
	}
	for _, raw := range envelope.Windows {
		win, err := decodeWindowIdentity(raw)
		if err != nil {
			return identity.ErrResourceStale
		}
		if win.WindowID == key.WindowID {
			if win.WindowKey == p.next.WindowKey {
				return nil
			}
			return identity.ErrResourceStale
		}
	}
	return &reportPresentationClosed{}
}

type reportPresentationClosed struct{}

func (*reportPresentationClosed) Error() string { return "report presentation window closed" }
func (*reportPresentationClosed) Unwrap() error { return identity.ErrResourceStale }

func (s *Service) activateReportPresentation(ctx context.Context, key windowPinKey, sessionID string, next windowResourcePin) (bool, error) {
	s.resourceMu.Lock()
	held := s.resourcePins[key]
	p := held.pendingReport
	s.resourceMu.Unlock()
	if p == nil {
		return false, nil
	}
	if sameReportCandidate(held.Resource, next.Resource) && !sameReportPin(p.next.Resource, next.Resource) {
		return false, nil // ordinary readmission of the still-active old report
	}
	if p.sessionID != sessionID {
		return true, identity.ErrResourceDenied
	}
	if p.next.Resource.URI == next.Resource.URI && p.next.Resource.ProviderIdentity == next.Resource.ProviderIdentity && p.next.Resource.ResourceCandidate != next.Resource.ResourceCandidate {
		return true, identity.ErrResourceStale
	}
	if !sameReportPin(p.next.Resource, next.Resource) {
		return true, identity.ErrResourceDenied
	}
	// Lock order is hub -> resource. The snapshot cannot close/change the slot
	// between the negative consistency check and activation. No I/O runs here.
	s.hub.mu.RLock()
	defer s.hub.mu.RUnlock()
	if err := s.presentationSnapshotLocked(key, p); err != nil {
		var closed *reportPresentationClosed
		if errors.As(err, &closed) {
			s.clearReportPresentation(key, p)
		}
		return true, err
	}
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	current := s.resourcePins[key]
	if current.pendingReport != p || !sameReportCandidate(current.Resource, p.old.Resource) || s.hub.clients[key.Namespace][key.ClientID] != p.session.receiver {
		return true, identity.ErrResourceStale
	}
	if err := p.ctx.Err(); err != nil {
		return true, err
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if !identity.MetadataExpiryValid(current.Resource.ValidUntil, time.Now()) || !identity.MetadataExpiryValid(next.Resource.ValidUntil, time.Now()) {
		return true, identity.ErrResourceStale
	}
	next.pendingReport = nil
	s.resourcePins[key] = next
	p.activated = true
	return true, nil
}

func (s *Service) clearReportPresentation(key windowPinKey, p *reportPresentation) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	held, ok := s.resourcePins[key]
	if ok && held.pendingReport == p {
		held.pendingReport = nil
		s.resourcePins[key] = held
	}
}

func (s *Service) finishReportPresentation(key windowPinKey, p *reportPresentation, output *UICommandOutput, err error) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	held, ok := s.resourcePins[key]
	if ok && held.pendingReport == p {
		held.pendingReport = nil
		s.resourcePins[key] = held
	}
	if output == nil && err == nil {
		err = errors.New("report presentation interrupted before acknowledgement")
	}
	if err != nil && p.activated {
		var bound interface{ ReportBindingActivated() bool }
		if !errors.As(err, &bound) {
			err = &reportBoundPresentationError{cause: err}
		}
	}
	p.output, p.err = output, err
	close(p.done)
	return err
}

func (s *Service) commitReportPresentation(key windowPinKey, p *reportPresentation, verified identity.ResolvedResource) error {
	s.hub.mu.RLock()
	defer s.hub.mu.RUnlock()
	if err := s.presentationSnapshotLocked(key, p); err != nil {
		return err
	}
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	held, ok := s.resourcePins[key]
	if !ok || s.hub.clients[key.Namespace][key.ClientID] != p.session.receiver {
		return identity.ErrResourceStale
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if !sameReportPin(p.next.Resource, verified) || !identity.MetadataExpiryValid(held.Resource.ValidUntil, time.Now()) {
		return identity.ErrResourceStale
	}
	if p.activated {
		if !sameReportCandidate(verified, held.Resource) {
			return identity.ErrResourceStale
		}
		// A separately readmitted owner keeps its binding. For the same
		// authority, neither an early bind nor its acknowledgement may extend
		// the lease returned by the other.
		if verified.AuthorityBinding == held.Resource.AuthorityBinding {
			held.Resource.ValidUntil = identity.MinimumMetadataExpiry(held.Resource.ValidUntil, verified.ValidUntil)
			s.resourcePins[key] = held
		}
		return nil
	}
	if held.pendingReport != p || !sameReportCandidate(held.Resource, p.old.Resource) {
		return identity.ErrResourceStale
	}
	next := p.next
	next.Resource = verified
	next.pendingReport = nil
	s.resourcePins[key] = next
	p.activated = true
	return nil
}

// ErrReportOpenUnconfirmed identifies a browser-acknowledged open whose
// final report identity or binding could not be confirmed.
var ErrReportOpenUnconfirmed = errors.New("The browser acknowledged the request. Check the open report before retrying.")

type reportBoundPresentationError struct{ cause error }

func (e *reportBoundPresentationError) Error() string {
	return "report binding activated; presentation could not be confirmed"
}
func (e *reportBoundPresentationError) Unwrap() error                { return e.cause }
func (e *reportBoundPresentationError) ReportBindingActivated() bool { return true }
