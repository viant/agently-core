package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/forge/backend/types"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport/server/base"
)

func presentationFixture(t *testing.T) (*Service, context.Context, windowPinKey, windowResourcePin, windowResourcePin) {
	t.Helper()
	s, ctx, _ := feedOwnershipFixture(t)
	ctx = context.WithValue(ctx, jsonrpc.SessionKey, &base.Session{Id: "presentation-session"})
	receiver := s.hub.registerHTTPClient("alice", "client-a", ctx)
	s.hub.httpStreamable()
	s.hub.httpBridge.bindSession(ctx, "client-a", "alice", nil, receiver)
	ctx = requestctx.WithUIBridgeSessionID(ctx, "presentation-session")
	ctx = requestctx.WithToolMessageID(ctx, "replace-one")
	key := windowPinKey{"alice", "client-a", "report-slot"}
	old := windowResourcePin{WindowKey: "report://example/original", Resource: identity.ResolvedResource{URI: "report://example/original", ProviderIdentity: "reports", AuthorityBinding: "selected", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte(`{"old":true}`))}, ValidUntil: time.Now().Add(time.Minute)}}
	next := old
	next.WindowKey = "report://example/personal"
	next.Resource.URI = next.WindowKey
	next.Resource.ContentFingerprint = identity.ContentFingerprint([]byte(`{"new":true}`))
	require.NoError(t, s.rememberWindowPin(ctx, key, old))
	presentationSnapshot(t, s, key, old.WindowKey)
	return s, ctx, key, old, next
}
func presentationSnapshot(t *testing.T, s *Service, key windowPinKey, uri string) {
	t.Helper()
	windows := []any{}
	if uri != "" {
		windows = append(windows, map[string]any{"windowId": key.WindowID, "windowKey": uri, "conversationId": "conv-a", "presentation": "hosted"})
	}
	raw, err := json.Marshal(map[string]any{"conversationId": "conv-a", "windows": windows})
	require.NoError(t, err)
	s.hub.setSnapshot(key.Namespace, key.ClientID, raw)
}
func presentationBind(s *Service, ctx context.Context, key windowPinKey, next windowResourcePin) error {
	_, err := s.BindAdmittedNativePresentationWindow(ctx, key.ClientID, key.WindowID, next.WindowKey, &types.Window{Resource: &next.Resource})
	return err
}
func TestReportPresentationActivatesBeforeAcknowledgementAndNeverRestoresEarlierPin(t *testing.T) {
	s, ctx, key, old, next := presentationFixture(t)
	require.ErrorIs(t, presentationBind(s, ctx, key, next), identity.ErrResourceDenied, "ordinary reads cannot switch resources")
	p, owner, err := s.beginReportPresentation(ctx, key, next, "request-one")
	require.NoError(t, err)
	require.True(t, owner)
	// Credential readmission of the old resource must not discard the pending open.
	refreshed := old
	refreshed.Resource.AuthorityBinding = "refreshed-old"
	require.NoError(t, presentationBind(s, ctx, key, refreshed))
	held, _ := s.getWindowPin(key)
	require.Same(t, p, held.pendingReport)
	presentationSnapshot(t, s, key, next.WindowKey)
	require.NoError(t, presentationBind(s, ctx, key, next))
	held, _ = s.getWindowPin(key)
	require.Equal(t, next.Resource, held.Resource)
	require.Nil(t, held.pendingReport)
	require.NoError(t, s.commitReportPresentation(key, p, p.next.Resource))
	later := next
	later.WindowKey = "report://example/later"
	later.Resource.URI = later.WindowKey
	second := requestctx.WithToolMessageID(ctx, "replace-two")
	p2, _, err := s.beginReportPresentation(second, key, later, "request-two")
	require.NoError(t, err)
	presentationSnapshot(t, s, key, later.WindowKey)
	require.NoError(t, presentationBind(s, ctx, key, later))
	require.ErrorIs(t, s.commitReportPresentation(key, p, p.next.Resource), identity.ErrResourceStale, "a late first acknowledgement cannot overwrite the second binding")
	s.finishReportPresentation(key, p, nil, identity.ErrResourceStale)
	held, _ = s.getWindowPin(key)
	require.Equal(t, later.Resource, held.Resource)
	s.finishReportPresentation(key, p2, nil, nil)
}
func TestReportPresentationCloseCancellationMismatchAndRetry(t *testing.T) {
	for _, kind := range []string{"close", "cancel", "expired", "candidate", "provider", "session", "conflict"} {
		t.Run(kind, func(t *testing.T) {
			s, ctx, key, old, next := presentationFixture(t)
			op, cancel := context.WithCancel(ctx)
			if kind == "expired" {
				cancel()
				op, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
			}
			defer cancel()
			p, _, err := s.beginReportPresentation(op, key, next, "request")
			require.NoError(t, err)
			presentationSnapshot(t, s, key, next.WindowKey)
			switch kind {
			case "close":
				presentationSnapshot(t, s, key, "")
				require.ErrorIs(t, presentationBind(s, ctx, key, next), identity.ErrResourceStale)
			case "cancel":
				cancel()
				require.ErrorIs(t, presentationBind(s, ctx, key, next), context.Canceled)
			case "expired":
				<-op.Done()
				require.ErrorIs(t, presentationBind(s, ctx, key, next), context.DeadlineExceeded)
			case "provider":
				changed := next
				changed.Resource.ProviderIdentity = "other-provider"
				require.ErrorIs(t, presentationBind(s, ctx, key, changed), identity.ErrResourceDenied)
			case "candidate":
				changed := next
				changed.Resource.ContentFingerprint = identity.ContentFingerprint([]byte(`{"edited":true}`))
				require.ErrorIs(t, presentationBind(s, ctx, key, changed), identity.ErrResourceStale)
			case "session":
				require.Error(t, presentationBind(s, requestctx.WithUIBridgeSessionID(ctx, "foreign-session"), key, next))
			case "conflict":
				_, _, err = s.beginReportPresentation(requestctx.WithToolMessageID(ctx, "other-op"), key, next, "request")
				require.ErrorIs(t, err, identity.ErrResourceStale)
				repeated, owner, err := s.beginReportPresentation(ctx, key, next, "request")
				require.NoError(t, err)
				require.False(t, owner)
				require.Same(t, p, repeated)
			}
			held, _ := s.getWindowPin(key)
			require.Equal(t, old.Resource, held.Resource)
			s.finishReportPresentation(key, p, nil, context.Canceled)
			require.ErrorIs(t, p.err, context.Canceled)
		})
	}
}
func TestReportPresentationActivatedCancellationRetainsBindingAndCause(t *testing.T) {
	s, ctx, key, _, next := presentationFixture(t)
	p, _, err := s.beginReportPresentation(ctx, key, next, "request")
	require.NoError(t, err)
	presentationSnapshot(t, s, key, next.WindowKey)
	require.NoError(t, presentationBind(s, ctx, key, next))
	failure := s.finishReportPresentation(key, p, nil, context.DeadlineExceeded)
	var bound interface{ ReportBindingActivated() bool }
	require.ErrorAs(t, failure, &bound)
	require.True(t, bound.ReportBindingActivated())
	require.ErrorIs(t, failure, context.DeadlineExceeded)
	held, _ := s.getWindowPin(key)
	require.Equal(t, next.Resource, held.Resource)
}
func TestNativeReportOpenPreflightCanBindBeforeUIAcknowledgement(t *testing.T) {
	for _, frameConversation := range []string{"conv-a", ""} {
		for _, sameURI := range []bool{false, true} {
			for _, accepted := range []bool{true, false} {
				t.Run(fmt.Sprintf("frame-owner-%s-same-uri-%t-accepted-%t", frameConversation, sameURI, accepted), func(t *testing.T) {
					s, ctx, key, old, next := presentationFixture(t)
					if sameURI {
						next.WindowKey = old.WindowKey
						next.Resource.URI = old.Resource.URI
					}
					raw, err := json.Marshal(map[string]any{"conversationId": "conv-a", "windows": []any{map[string]any{"windowId": key.WindowID, "windowKey": "report://example/original", "conversationId": frameConversation, "region": "app.section"}}})
					require.NoError(t, err)
					s.hub.setSnapshot(key.Namespace, key.ClientID, raw)
					ctx = context.WithValue(ctx, reportInstancePrincipalKey{}, reportInstancePrincipal{"alice", "selected"})
					s.cfg.ReportCatalog = &reportInstanceCatalog{pin: next.Resource}
					done := make(chan error, 1)
					go func() {
						command, err := s.hub.dequeueCommand(ctx, "alice", "client-a")
						if err == nil {
							var args struct {
								Options struct {
									ConversationID string `json:"conversationId"`
								} `json:"options"`
							}
							encoded, encodeErr := json.Marshal(command.Params)
							require.NoError(t, encodeErr)
							require.NoError(t, json.Unmarshal(encoded, &args))
							require.Equal(t, frameConversation, args.Options.ConversationID)
							presentationSnapshot(t, s, key, next.WindowKey)
							selected, readErr := s.ReportDefinition(ctx, &ReportDefinitionInput{Resource: identity.ResourceRef{URI: next.WindowKey}})
							if readErr != nil {
								err = readErr
							} else {
								err = presentationBind(s, ctx, key, windowResourcePin{WindowKey: next.WindowKey, Resource: *selected.Resource})
							}
							if err == nil {
								s.hub.deliverResponse(&rpcResponse{ID: command.ID, OK: accepted, Error: "synthetic presentation rejection", Result: json.RawMessage(`{"windowId":"report-slot"}`)})
							}
						}
						done <- err
					}()
					output, err := s.UICommand(ctx, &UICommandInput{ClientID: key.ClientID, Namespace: key.Namespace, Method: "ui.window.open", TimeoutMs: 1000, Params: map[string]any{"windowKey": next.WindowKey, "windowId": key.WindowID, "expectedRevision": 17, "options": map[string]any{"conversationId": "conv-a"}}})
					if accepted {
						require.NoError(t, err)
						require.True(t, output.OK)
					} else {
						require.Nil(t, output)
						require.Error(t, err)
						var activated interface{ ReportBindingActivated() bool }
						require.ErrorAs(t, err, &activated)
						require.True(t, activated.ReportBindingActivated())
					}
					require.NoError(t, <-done)
					held, _ := s.getWindowPin(key)
					require.Equal(t, next.Resource, held.Resource)
					require.Nil(t, held.pendingReport)
					require.False(t, errors.Is(err, context.DeadlineExceeded))

				})
			}
		}
	}
}
func TestReportPresentationCancellationActivationRaceNeverRollsBack(t *testing.T) {
	for i := 0; i < 20; i++ {
		s, ctx, key, old, next := presentationFixture(t)
		op, cancel := context.WithCancel(ctx)
		p, _, err := s.beginReportPresentation(op, key, next, "request")
		require.NoError(t, err)
		presentationSnapshot(t, s, key, next.WindowKey)
		start := make(chan struct{})
		finished := make(chan error, 1)
		canceled := make(chan struct{})
		go func() { <-start; cancel(); close(canceled) }()
		go func() { <-start; finished <- presentationBind(s, ctx, key, next) }()
		close(start)
		err = <-finished
		<-canceled
		held, _ := s.getWindowPin(key)
		if err == nil {
			require.Equal(t, next.Resource, held.Resource)
		} else {
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, old.Resource, held.Resource)
		}
		s.finishReportPresentation(key, p, nil, err)
		after, _ := s.getWindowPin(key)
		require.Equal(t, held.Resource, after.Resource)
	}
}

func TestReportPresentationEarlySnapshotDoesNotDiscardPending(t *testing.T) {
	s, ctx, key, _, next := presentationFixture(t)
	p, _, err := s.beginReportPresentation(ctx, key, next, "request")
	require.NoError(t, err)
	require.ErrorIs(t, presentationBind(s, ctx, key, next), identity.ErrResourceStale)
	held, _ := s.getWindowPin(key)
	require.Same(t, p, held.pendingReport)
	presentationSnapshot(t, s, key, next.WindowKey)
	require.NoError(t, presentationBind(s, ctx, key, next))
	refreshed := next
	refreshed.Resource.AuthorityBinding = "renewed-native-owner"
	require.NoError(t, presentationBind(s, ctx, key, refreshed))
	require.NoError(t, s.commitReportPresentation(key, p, p.next.Resource))
	held, _ = s.getWindowPin(key)
	require.Equal(t, refreshed.Resource, held.Resource)
	s.finishReportPresentation(key, p, &UICommandOutput{OK: true}, nil)
}
func TestNativeReportOpenRejectsUnfencedPinnedSlotSwitch(t *testing.T) {
	s, ctx, key, old, next := presentationFixture(t)
	ctx = context.WithValue(ctx, reportInstancePrincipalKey{}, reportInstancePrincipal{"alice", "selected"})
	s.cfg.ReportCatalog = &reportInstanceCatalog{pin: next.Resource}
	_, err := s.UICommand(ctx, &UICommandInput{ClientID: key.ClientID, Method: "ui.window.open", Params: map[string]any{"windowId": key.WindowID, "windowKey": next.WindowKey, "options": map[string]any{"conversationId": "conv-a"}}})
	require.ErrorIs(t, err, identity.ErrResourceStale)
	held, _ := s.getWindowPin(key)
	require.Equal(t, old.Resource, held.Resource)
}
func TestReportPresentationFinishFencesLateActivationAndPanicJoiner(t *testing.T) {
	s, ctx, key, old, next := presentationFixture(t)
	p, _, err := s.beginReportPresentation(ctx, key, next, "request")
	require.NoError(t, err)
	failure := s.finishReportPresentation(key, p, nil, nil)
	require.Error(t, failure)
	require.Error(t, p.err)
	presentationSnapshot(t, s, key, next.WindowKey)
	require.ErrorIs(t, presentationBind(s, ctx, key, next), identity.ErrResourceDenied)
	held, _ := s.getWindowPin(key)
	require.Equal(t, old.Resource, held.Resource)
}

func TestUnfencedReportAcknowledgementCannotRestoreEarlierBinding(t *testing.T) {
	s, ctx, key, old, next := presentationFixture(t)
	ctx = context.WithValue(ctx, reportInstancePrincipalKey{}, reportInstancePrincipal{"alice", "selected"})
	s.cfg.ReportCatalog = &reportInstanceCatalog{pin: old.Resource}
	done := make(chan error, 1)
	go func() {
		command, err := s.hub.dequeueCommand(ctx, key.Namespace, key.ClientID)
		if err == nil {
			err = s.rememberWindowPin(ctx, key, next)
			s.hub.deliverResponse(&rpcResponse{ID: command.ID, OK: true, Result: json.RawMessage(`{"windowId":"report-slot"}`)})
		}
		done <- err
	}()
	_, err := s.UICommand(ctx, &UICommandInput{ClientID: key.ClientID, Method: "ui.window.open", TimeoutMs: 1000, Params: map[string]any{"windowKey": old.WindowKey, "windowId": key.WindowID, "options": map[string]any{"conversationId": "conv-a"}}})
	require.ErrorIs(t, err, identity.ErrResourceStale)
	require.NoError(t, <-done)
	held, _ := s.getWindowPin(key)
	require.Equal(t, next.Resource, held.Resource)
}

func TestReportPresentationCloseObservedBeforeCommitCannotActivate(t *testing.T) {
	s, ctx, key, old, next := presentationFixture(t)
	p, _, err := s.beginReportPresentation(ctx, key, next, "close-before-commit")
	require.NoError(t, err)
	presentationSnapshot(t, s, key, next.WindowKey)
	presentationSnapshot(t, s, key, "")
	err = s.commitReportPresentation(key, p, p.next.Resource)
	require.ErrorIs(t, err, identity.ErrResourceStale)
	s.finishReportPresentation(key, p, nil, err)
	held, _ := s.getWindowPin(key)
	require.Equal(t, old.Resource, held.Resource)
	require.False(t, p.activated)
	require.Nil(t, held.pendingReport)
}

func TestReportPresentationExpiredOldPinCannotActivateFreshCandidate(t *testing.T) {
	s, ctx, key, old, next := presentationFixture(t)
	old.Resource.ValidUntil = time.Now().Add(30 * time.Millisecond)
	require.NoError(t, s.rememberWindowPin(ctx, key, old))
	p, _, err := s.beginReportPresentation(ctx, key, next, "old-expiry")
	require.NoError(t, err)
	<-time.After(time.Until(old.Resource.ValidUntil) + time.Millisecond)
	presentationSnapshot(t, s, key, next.WindowKey)
	require.ErrorIs(t, presentationBind(s, ctx, key, next), identity.ErrResourceStale)
	err = s.commitReportPresentation(key, p, p.next.Resource)
	require.ErrorIs(t, err, identity.ErrResourceStale)
	s.finishReportPresentation(key, p, nil, err)
	require.False(t, p.activated)
	held, _ := s.getWindowPin(key)
	require.Equal(t, old.Resource, held.Resource)
	require.Nil(t, held.pendingReport)
}

// The catalog is healthy for admission and fails only when the acknowledgement
// requires a fresh owning read. This exercises the real UI command boundary.
type reportAcknowledgementReadFailure struct {
	*reportInstanceCatalog
	readsAfterAdmission int
	cause               error
}

func (c *reportAcknowledgementReadFailure) Read(ctx context.Context, ref identity.ResourceRef, pin *identity.ResolvedResource) ([]byte, *identity.ResolvedResource, error) {
	c.readsAfterAdmission++
	if c.readsAfterAdmission > 1 {
		return nil, nil, c.cause
	}
	return c.reportInstanceCatalog.Read(ctx, ref, pin)
}
func TestNativeReportAcknowledgementRetainsOwningReadFailure(t *testing.T) {
	unavailable := errors.New("synthetic metadata dependency unavailable")
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, identity.ErrResourceStale, unavailable} {
		t.Run(cause.Error(), func(t *testing.T) {
			s, ctx, key, old, next := presentationFixture(t)
			ctx = context.WithValue(ctx, reportInstancePrincipalKey{}, reportInstancePrincipal{"alice", "selected"})
			catalog := &reportAcknowledgementReadFailure{reportInstanceCatalog: &reportInstanceCatalog{pin: next.Resource}, cause: cause}
			s.cfg.ReportCatalog = catalog
			delivered := make(chan error, 1)
			go func() {
				command, err := s.hub.dequeueCommand(ctx, key.Namespace, key.ClientID)
				if err == nil {
					s.hub.deliverResponse(&rpcResponse{ID: command.ID, OK: true, Result: json.RawMessage(`{"windowId":"report-slot"}`)})
				}
				delivered <- err
			}()
			output, err := s.UICommand(ctx, &UICommandInput{ClientID: key.ClientID, Namespace: key.Namespace, Method: "ui.window.open", TimeoutMs: 1000, Params: map[string]any{"windowKey": next.WindowKey, "windowId": key.WindowID, "expectedRevision": 17, "options": map[string]any{"conversationId": "conv-a"}}})
			require.NoError(t, <-delivered)
			require.Nil(t, output)
			require.ErrorIs(t, err, cause)
			require.ErrorIs(t, err, ErrReportOpenUnconfirmed)
			require.NotErrorIs(t, err, identity.ErrResourceDenied)
			held, _ := s.getWindowPin(key)
			require.Equal(t, old.Resource, held.Resource)
			require.Nil(t, held.pendingReport)
			require.Equal(t, 2, catalog.readsAfterAdmission)
		})
	}
}

func TestLegacyReportReplacementWithoutNativeFenceRejectsBeforeUICommand(t *testing.T) {
	s, ctx, key, old, next := presentationFixture(t)
	ctx = context.WithValue(ctx, reportInstancePrincipalKey{}, reportInstancePrincipal{"alice", "selected"})
	s.cfg.ReportCatalog = &reportInstanceCatalog{pin: next.Resource}
	s.hub.httpMu.Lock()
	s.hub.httpBridge = nil
	s.ns.Resolver = nil
	s.ns.DefaultNamespace = key.Namespace
	s.hub.httpMu.Unlock()
	output, err := s.UICommand(ctx, &UICommandInput{ClientID: key.ClientID, Namespace: key.Namespace, Method: "ui.window.open", TimeoutMs: 10, Params: map[string]any{"windowKey": next.WindowKey, "windowId": key.WindowID, "expectedRevision": 17, "options": map[string]any{"conversationId": "conv-a"}}})
	require.Nil(t, output)
	require.ErrorIs(t, err, identity.ErrResourceStale)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	held, _ := s.getWindowPin(key)
	require.Equal(t, old.Resource, held.Resource)
	require.Nil(t, held.pendingReport)
	poll, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	_, err = s.hub.dequeueCommand(poll, key.Namespace, key.ClientID)
	require.ErrorIs(t, err, context.DeadlineExceeded, "no command was sent to the UI")
}

func TestUnfencedReportReopenRetainsIndependentFrameOwnership(t *testing.T) {
	s, ctx, key, old, _ := presentationFixture(t)
	ctx = context.WithValue(ctx, reportInstancePrincipalKey{}, reportInstancePrincipal{"alice", "selected"})
	s.cfg.ReportCatalog = &reportInstanceCatalog{pin: old.Resource}
	raw, err := json.Marshal(map[string]any{"conversationId": "conv-a", "windows": []any{map[string]any{"windowId": key.WindowID, "windowKey": old.WindowKey, "conversationId": "", "region": "app.section"}}})
	require.NoError(t, err)
	s.hub.setSnapshot(key.Namespace, key.ClientID, raw)
	delivered := make(chan map[string]any, 1)
	go func() {
		command, err := s.hub.dequeueCommand(ctx, key.Namespace, key.ClientID)
		if err != nil {
			delivered <- nil
			return
		}
		encoded, _ := json.Marshal(command.Params)
		var args map[string]any
		_ = json.Unmarshal(encoded, &args)
		delivered <- args
		s.hub.deliverResponse(&rpcResponse{ID: command.ID, OK: true, Result: json.RawMessage(`{"windowId":"report-slot"}`)})
	}()
	output, err := s.UICommand(ctx, &UICommandInput{ClientID: key.ClientID, Namespace: key.Namespace, Method: "ui.window.open", TimeoutMs: 1000, Params: map[string]any{"windowKey": old.WindowKey, "windowId": key.WindowID, "options": map[string]any{"conversationId": "conv-a"}}})
	require.NoError(t, err)
	require.True(t, output.OK)
	args := <-delivered
	require.NotNil(t, args)
	require.Equal(t, "", args["options"].(map[string]any)["conversationId"])
	held, _ := s.getWindowPin(key)
	require.Equal(t, old.Resource, held.Resource)
}

func TestReportReplacementRejectsForeignOrMissingFrameBeforeReportRead(t *testing.T) {
	for _, kind := range []string{"foreign", "missing"} {
		t.Run(kind, func(t *testing.T) {
			s, ctx, key, old, next := presentationFixture(t)
			ctx = context.WithValue(ctx, reportInstancePrincipalKey{}, reportInstancePrincipal{"alice", "selected"})
			catalog := &reportInstanceCatalog{pin: next.Resource}
			s.cfg.ReportCatalog = catalog
			windows := []any{}
			if kind == "foreign" {
				windows = append(windows, map[string]any{"windowId": key.WindowID, "windowKey": old.WindowKey, "conversationId": "other-conversation"})
			}
			raw, err := json.Marshal(map[string]any{"conversationId": "conv-a", "windows": windows})
			require.NoError(t, err)
			s.hub.setSnapshot(key.Namespace, key.ClientID, raw)
			output, err := s.UICommand(ctx, &UICommandInput{ClientID: key.ClientID, Namespace: key.Namespace, Method: "ui.window.open", TimeoutMs: 10, Params: map[string]any{"windowKey": next.WindowKey, "windowId": key.WindowID, "expectedRevision": 17, "options": map[string]any{"conversationId": "conv-a"}}})
			require.Nil(t, output)
			if kind == "foreign" {
				require.ErrorIs(t, err, identity.ErrResourceDenied)
			} else {
				require.ErrorIs(t, err, identity.ErrResourceStale)
			}
			require.Zero(t, catalog.reads, "invalid frame targets must not fetch report metadata")
			held, _ := s.getWindowPin(key)
			require.Equal(t, old.Resource, held.Resource)
			require.Nil(t, held.pendingReport)
			poll, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
			defer cancel()
			_, err = s.hub.dequeueCommand(poll, key.Namespace, key.ClientID)
			require.ErrorIs(t, err, context.DeadlineExceeded)
		})
	}
}

type shortenedReadbackCatalog struct {
	*reportInstanceCatalog
	until time.Time
}

func (c *shortenedReadbackCatalog) Read(ctx context.Context, ref identity.ResourceRef, pin *identity.ResolvedResource) ([]byte, *identity.ResolvedResource, error) {
	raw, fresh, err := c.reportInstanceCatalog.Read(ctx, ref, pin)
	if err == nil && pin != nil {
		renewed := *fresh
		renewed.ValidUntil = c.until
		fresh = &renewed
	}
	return raw, fresh, err
}
func TestReportOpenRetainsShortenedReadbackLease(t *testing.T) {
	for _, mode := range []string{"new", "replacement", "early-activation"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, slot, _, next := presentationFixture(t)
			ctx = context.WithValue(ctx, reportInstancePrincipalKey{}, reportInstancePrincipal{"alice", "selected"})
			until := time.Now().Add(10 * time.Second)
			s.cfg.ReportCatalog = &shortenedReadbackCatalog{reportInstanceCatalog: &reportInstanceCatalog{pin: next.Resource}, until: until}
			params := map[string]any{"windowKey": next.WindowKey, "options": map[string]any{"conversationId": "conv-a"}}
			if mode != "new" {
				params["windowId"] = slot.WindowID
				params["expectedRevision"] = 17
			} else {
				slot.WindowID = "new-slot"
			}
			answered := make(chan error, 1)
			go func() {
				command, err := s.hub.dequeueCommand(ctx, slot.Namespace, slot.ClientID)
				if err == nil {
					presentationSnapshot(t, s, slot, next.WindowKey)
					if mode == "early-activation" {
						err = presentationBind(s, ctx, slot, next)
					}
					ack, _ := json.Marshal(map[string]string{"windowId": slot.WindowID})
					s.hub.deliverResponse(&rpcResponse{ID: command.ID, OK: true, Result: ack})
				}
				answered <- err
			}()
			_, err := s.UICommand(ctx, &UICommandInput{ClientID: slot.ClientID, Namespace: slot.Namespace, Method: "ui.window.open", TimeoutMs: 2000, Params: params})
			require.NoError(t, <-answered)
			require.NoError(t, err)
			held, ok := s.getWindowPin(slot)
			require.True(t, ok)
			require.Equal(t, until, held.Resource.ValidUntil)
		})
	}
}

func TestRenewedHeldReportSupportsReplacementAfterOriginalLeaseExpires(t *testing.T) {
	for _, early := range []bool{true, false} {
		t.Run(fmt.Sprintf("preflight-%t", early), func(t *testing.T) {
			s, ctx, key, old, next := presentationFixture(t)
			old.Resource.ValidUntil = time.Now().Add(30 * time.Millisecond)
			require.NoError(t, s.rememberWindowPin(ctx, key, old))
			p, _, err := s.beginReportPresentation(ctx, key, next, "renewed-owner")
			require.NoError(t, err)
			renewed := old
			renewed.Resource.ValidUntil = next.Resource.ValidUntil
			renewed.Resource.AuthorityBinding = "renewed-owner"
			require.NoError(t, presentationBind(s, ctx, key, renewed))
			<-time.After(time.Until(old.Resource.ValidUntil) + time.Millisecond)
			presentationSnapshot(t, s, key, next.WindowKey)
			if early {
				require.NoError(t, presentationBind(s, ctx, key, next))
			}
			require.NoError(t, s.commitReportPresentation(key, p, next.Resource))
			require.NoError(t, s.finishReportPresentation(key, p, &UICommandOutput{OK: true}, nil))
			held, _ := s.getWindowPin(key)
			require.Equal(t, next.Resource, held.Resource)
		})
	}
}
