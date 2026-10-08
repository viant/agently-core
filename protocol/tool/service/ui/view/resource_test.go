package view

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/viant/afs"
	contexttool "github.com/viant/agently-core/protocol/tool/service/ui/context"
	datasourcetool "github.com/viant/agently-core/protocol/tool/service/ui/datasource"
	eventstool "github.com/viant/agently-core/protocol/tool/service/ui/events"
	windowtool "github.com/viant/agently-core/protocol/tool/service/ui/window"
	"github.com/viant/agently-core/runtime/requestctx"
	repo "github.com/viant/agently-core/workspace/repository/forgewindow"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/service/meta"
)

type lifecyclePolicy struct {
	mu               sync.Mutex
	binding, omitted string
	allowed          bool
}

func (p *lifecyclePolicy) update(binding, omitted string, allowed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.binding, p.omitted, p.allowed = binding, omitted, allowed
}
func (p *lifecyclePolicy) SelectRevision(_ context.Context, ref identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	selector := p.omitted
	if ref.Revision != "" {
		selector = ref.Revision
	}
	if p.allowed && (selector == "1" || selector == identity.WorkingCandidate) {
		for _, candidate := range c {
			if candidate.Selector() == selector {
				return identity.ResourceDecision{Candidate: candidate, AuthorityBinding: p.binding, ValidUntil: time.Now().Add(time.Minute)}, nil
			}
		}
	}
	return identity.ResourceDecision{}, identity.ErrResourceDenied
}

type lifecycleSource struct {
	mu  sync.Mutex
	raw map[string]json.RawMessage
}

func (s *lifecycleSource) Candidates(context.Context, identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []identity.ResourceCandidate
	for selector, raw := range s.raw {
		kind, stamp := identity.StampedCandidate, selector
		if selector == identity.WorkingCandidate {
			kind, stamp = identity.WorkingCandidate, ""
		}
		result = append(result, identity.ResourceCandidate{Kind: kind, Revision: stamp, ContentFingerprint: identity.ContentFingerprint(raw)})
	}
	return result, nil
}
func (s *lifecycleSource) ReadCandidate(_ context.Context, _ identity.ResourceURI, c identity.ResourceCandidate) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(json.RawMessage(nil), s.raw[c.Selector()]...), nil
}
func (s *lifecycleSource) setWorking(raw json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raw[identity.WorkingCandidate] = raw
}
func canonicalLifecycleFixture(t *testing.T, root string) (*Service, *forgeuisvc.Service, *lifecyclePolicy, *lifecycleSource, context.Context) {
	t.Helper()
	mustWriteFile(t, filepath.Join(root, "extension", "forge", "windows", "overview.yaml"), "id: overview\nwindowKey: overview\ntitle: Overview\npresentation: hosted\nregion: chat.top\nrefreshOnOpen: false\n")
	policy := &lifecyclePolicy{binding: "alice/account/identity-1", omitted: "forbidden-default", allowed: true}
	source := &lifecycleSource{raw: map[string]json.RawMessage{"1": json.RawMessage(`{"windowKey":"overview","view":{"content":{"id":"approved-stamp"}},"dataSource":{"rows":{}}}`), identity.WorkingCandidate: json.RawMessage(`{"windowKey":"overview","view":{"content":{"id":"approved-working"}},"dataSource":{"rows":{}}}`)}}
	resolver := &identity.ResourceResolver{Source: source, Policy: policy}
	catalog, err := forgeuisvc.NewMetadataWindowCatalog(meta.New(afs.New(), root), root, []forgeuisvc.SavedWindow{{WindowDefinitionSummary: forgeuisvc.WindowDefinitionSummary{WindowID: "overview", Title: "Overview", ResourceURI: "window://steward/overview"}, Key: "must-not-load-latest"}}, forgeuisvc.WithWindowResourceResolver(func(context.Context, string) (*identity.ResourceResolver, identity.ResourceRef, error) {
		return resolver, identity.ResourceRef{URI: "window://steward/overview"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{WindowDefinitions: catalog, DynamicWindowAuthorizer: func(context.Context, string) (bool, error) { return true, nil }, ResolvedWindowAuthorizer: func(ctx context.Context, pin identity.ResolvedResource, method string, params map[string]any) error {
		if trusted, ok := requestctx.ResolvedResourceFromContext(ctx); ok && (trusted.ResourceCandidate != pin.ResourceCandidate || trusted.AuthorityBinding != pin.AuthorityBinding) {
			return identity.ErrResourceDenied
		}
		return nil
	}})
	postUIRPC(t, bridge, "ui.hello", map[string]any{"clientId": "client"})
	postUIRPC(t, bridge, "ui.snapshot", map[string]any{"clientId": "client", "data": map[string]any{"conversationId": "conversation", "clientId": "client", "windows": []any{map[string]any{"windowId": "chat/new", "windowKey": "chat/new", "conversationId": "conversation"}}}})
	ctx := requestctx.WithConversationID(context.Background(), "conversation")
	return New(repo.New(afs.New()), bridge), bridge, policy, source, ctx
}
func serveCanonicalOpen(t *testing.T, bridge *forgeuisvc.Service) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		result := postUIRPC(t, bridge, "ui.poll", map[string]any{"clientId": "client", "timeoutMs": 2000})
		req, ok := result["params"].(map[string]any)
		if !ok {
			done <- fmt.Errorf("missing command envelope")
			return
		}
		params, ok := req["params"].(map[string]any)
		if !ok {
			done <- fmt.Errorf("missing open params")
			return
		}
		options, _ := params["options"].(map[string]any)
		metadata, _ := options["inlineMetadata"].(map[string]any)
		pin, _ := params["resource"].(map[string]any)
		if metadata == nil || pin["revision"] != "1" {
			done <- fmt.Errorf("open did not preserve approved historical pin: %+v", params)
			return
		}
		windowID, _ := params["windowId"].(string)
		postUIRPC(t, bridge, "ui.snapshot", map[string]any{"clientId": "client", "data": map[string]any{"conversationId": "conversation", "selected": map[string]any{"windowId": windowID}, "windows": []any{map[string]any{"windowId": "chat/new", "windowKey": "chat/new"}, map[string]any{"windowId": windowID, "windowKey": "overview", "conversationId": "conversation", "presentation": "hosted", "resource": map[string]any{"uri": "window://foreign/forged"}, "metadata": map[string]any{"view": map[string]any{"content": map[string]any{"id": "forged-browser"}}}, "dataSources": map[string]any{"rows": map[string]any{"collection": []any{map[string]any{"synthetic": 1}}}, "forgedSource": map[string]any{"collection": []any{map[string]any{"secret": "hidden"}}}}}}}})
		postUIRPC(t, bridge, "ui.response", map[string]any{"id": req["id"], "ok": true, "result": map[string]any{"windowId": windowID}})
		done <- nil
	}()
	return done
}
func TestExistingUIRoutesKeepHistoricalPinAndRecheckArchivedEvents(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		views, bridge, policy, _, ctx := canonicalLifecycleFixture(t, root)
		listed := &ListOutput{}
		if err := views.list(ctx, &ListInput{}, listed); err != nil || len(listed.Items) != 0 {
			t.Fatalf("omitted denial list=%+v %v", listed, err)
		}
		ref := &identity.ResourceRef{URI: "window://steward/overview", Revision: "1"}
		got := &GetOutput{}
		if err := views.get(ctx, &GetInput{Resource: ref}, got); err != nil || got.Item.Resource.Revision != "1" {
			t.Fatalf("explicit URI get=%+v %v", got, err)
		}
		if err := views.get(ctx, &GetInput{ID: "overview", Resource: &identity.ResourceRef{URI: ref.URI, Revision: "2"}}, &GetOutput{}); err == nil {
			t.Fatal("explicit forbidden get served")
		}
		done := serveCanonicalOpen(t, bridge)
		opened := &OpenOutput{}
		if err := views.open(ctx, &OpenInput{Resource: ref, ClientID: "client", TimeoutMs: 2000}, opened); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if opened.Resource == nil || opened.Resource.Revision != "1" || opened.WindowID == "" {
			t.Fatalf("open pin=%+v", opened)
		}
		windows := windowtool.New(bridge)
		getWindow, _ := windows.Method("get")
		windowOut := &windowtool.GetOutput{}
		if err := getWindow(ctx, &windowtool.GetInput{ClientID: "client", WindowID: opened.WindowID, Resource: opened.Resource}, windowOut); err != nil || windowOut.Window.Resource.URI != ref.URI {
			t.Fatalf("live get=%+v %v", windowOut, err)
		}
		raw, _ := json.Marshal(windowOut.Window.Metadata)
		if string(raw) == "null" || string(raw) == "{}" || strings.Contains(string(raw), "forged-browser") {
			t.Fatalf("browser metadata remained authority: %s", raw)
		}
		sources := datasourcetool.New(bridge)
		listSources, _ := sources.Method("list")
		sourceOut := &datasourcetool.ListOutput{}
		if err := listSources(ctx, &datasourcetool.ListInput{ClientID: "client", WindowID: opened.WindowID, Resource: opened.Resource}, sourceOut); err != nil || len(sourceOut.DataSourceRefs) != 1 || sourceOut.DataSourceRefs[0] != "rows" {
			t.Fatalf("approved datasource list=%+v %v", sourceOut, err)
		}
		peek, _ := sources.Method("peek")
		peekOut := &datasourcetool.PeekOutput{}
		if err := peek(ctx, &datasourcetool.PeekInput{ClientID: "client", WindowID: opened.WindowID, DataSourceRef: "rows", Resource: opened.Resource}, peekOut); err != nil || peekOut.Resource.Revision != "1" {
			t.Fatalf("pinned peek=%+v %v", peekOut, err)
		}
		events, err := views.reg.ListEventsContext(ctx, "conversation", "client", opened.WindowID, "overview", 10, 0)
		if err != nil || len(events) == 0 {
			t.Fatalf("trusted archived event=%+v %v", events, err)
		}
		postUIRPC(t, bridge, "ui.snapshot", map[string]any{"clientId": "client", "data": map[string]any{"conversationId": "conversation", "windows": []any{map[string]any{"windowId": "chat/new", "windowKey": "chat/new"}}}})
		if _, _, _, win, err := views.reg.FindReadableWindow(ctx, "conversation", "client", opened.WindowID, "overview"); err != nil || win.Resource == nil {
			t.Fatalf("authorized event fallback=%+v %v", win, err)
		}
		policy.update("alice/other-account/identity-1", "forbidden-default", true)
		if err := getWindow(ctx, &windowtool.GetInput{ClientID: "client", WindowID: opened.WindowID}, &windowtool.GetOutput{}); err == nil {
			t.Fatal("account-switched archive resurrected")
		}
		if events, err := views.reg.ListEventsContext(ctx, "conversation", "client", opened.WindowID, "overview", 10, 0); err != nil || len(events) != 0 {
			t.Fatalf("changed-account events disclosed=%+v %v", events, err)
		}
		contexts := contexttool.New(bridge)
		getContext, _ := contexts.Method("get")
		contextOut := &contexttool.GetOutput{}
		if err := getContext(ctx, &contexttool.GetInput{ClientID: "client", WindowID: opened.WindowID}, contextOut); err != nil || len(contextOut.Windows) != 0 || len(contextOut.RecentEvents) != 0 || len(contextOut.CurrentReports) != 0 {
			t.Fatalf("revoked context disclosed=%+v %v", contextOut, err)
		}
		recorder := eventstool.New(bridge)
		record, _ := recorder.Method("record")
		if err := record(ctx, &eventstool.RecordInput{ClientID: "client", WindowID: opened.WindowID, WindowKey: "overview", Kind: "report.context_updated", Detail: map[string]any{"artifactRef": "forged"}}, &eventstool.RecordOutput{}); err == nil {
			t.Fatal("revoked archived identity accepted browser event")
		}
	})
}
func TestPreparedWorkingOpenRejectsChangedBytesBeforeDispatch(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		views, bridge, policy, source, ctx := canonicalLifecycleFixture(t, root)
		policy.update("alice/account/identity-1", identity.WorkingCandidate, true)
		prepared, err := views.prepareOpenItem(ctx, OpenItem{ID: "overview", Resource: &identity.ResourceRef{URI: "window://steward/overview", Revision: identity.WorkingCandidate}})
		if err != nil {
			t.Fatal(err)
		}
		source.setWorking(json.RawMessage(`{"windowKey":"overview","view":{"content":{"id":"changed"}},"dataSource":{"rows":{}}}`))
		if _, err := views.openPreparedItem(ctx, "client", "default", "conversation", prepared, 500); err == nil {
			t.Fatal("prepared working pin re-resolved changed bytes")
		}
		assertNoUICommand(t, bridge, "client")
	})
}
