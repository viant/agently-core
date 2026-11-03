package view

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	identity "github.com/viant/agently-core/protocol/resource"
	windowtool "github.com/viant/agently-core/protocol/tool/service/ui/window"
	"github.com/viant/agently-core/runtime/requestctx"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/reporting/catalog"
	repo "github.com/viant/agently-core/workspace/repository/forgewindow"
)

type reportOpenWindowCatalog struct{}

func (*reportOpenWindowCatalog) UsesResourceResolution() bool { return true }
func (*reportOpenWindowCatalog) List(context.Context, *forgeuisvc.WindowDefinitionListInput) (*forgeuisvc.WindowDefinitionListOutput, error) {
	return &forgeuisvc.WindowDefinitionListOutput{}, nil
}
func (*reportOpenWindowCatalog) Get(context.Context, *forgeuisvc.WindowDefinitionGetInput) (*forgeuisvc.WindowDefinitionGetOutput, error) {
	return nil, identity.ErrResourceDenied
}

type reportOpenPolicy struct {
	allowed bool
	changed bool
}

func (p *reportOpenPolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if !p.allowed || len(candidates) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	binding := "example-actor"
	if p.changed {
		binding = "changed-actor"
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: binding, ValidUntil: time.Now().Add(time.Minute)}, nil
}
func reportOpenFixture(t *testing.T) (*Service, *forgeuisvc.Service, *reportOpenPolicy, context.Context) {
	t.Helper()
	uri, err := identity.ParseResourceURI("report://example/summary")
	require.NoError(t, err)
	raw := json.RawMessage(`{"schemaVersion":1,"format":"forge.authoredReport","reportDocument":{"title":"Summary","blocks":[]},"builderRef":"authored"}`)
	policy := &reportOpenPolicy{allowed: true}
	source := &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return raw, nil }}
	resolver := &identity.ResourceResolver{Source: source, Policy: policy}
	actor := identity.VerifiedActor{Subject: "example-user", Issuer: "example-issuer", TenantID: "example-tenant", AccountID: "example-account", IdentityRevision: "one", ValidUntil: time.Now().Add(time.Minute)}
	reports := &catalog.ReportCatalogService{Identity: func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, Resolver: func(context.Context) (*identity.ResourceResolver, error) { return resolver, nil }}
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{WindowDefinitions: &reportOpenWindowCatalog{}, ReportCatalog: reports})
	postUIRPC(t, bridge, "ui.hello", map[string]any{"clientId": "client"})
	postUIRPC(t, bridge, "ui.snapshot", map[string]any{"clientId": "client", "data": map[string]any{"conversationId": "conversation", "clientId": "client", "windows": []any{map[string]any{"windowId": "chat/new", "windowKey": "chat/new", "conversationId": "conversation"}}}})
	return New(repo.New(afs.New()), bridge), bridge, policy, requestctx.WithConversationID(t.Context(), "conversation")
}

func TestOpenExistingReportResourceUsesSingleNativeCommand(t *testing.T) {
	views, bridge, _, ctx := reportOpenFixture(t)
	for _, parameters := range []map[string]any{{}, {"entityId": 17, "from": "2026-01-01", "to": "2026-02-01", "regions": []any{"north"}, "executeOnOpen": true, "customProducerInput": false}} {
		delivered := make(chan map[string]any, 1)
		done := make(chan error, 1)
		go func() {
			response := postUIRPC(t, bridge, "ui.poll", map[string]any{"clientId": "client", "timeoutMs": 2000})
			command, ok := response["params"].(map[string]any)
			if !ok {
				done <- fmt.Errorf("no native command")
				return
			}
			args, ok := command["params"].(map[string]any)
			if !ok {
				done <- fmt.Errorf("no command input")
				return
			}
			delivered <- args
			postUIRPC(t, bridge, "ui.response", map[string]any{"id": command["id"], "ok": true, "result": map[string]any{"windowId": args["windowId"], "workspaceObject": map[string]any{"lifecycle": map[string]any{"state": "opening"}}}})
			done <- nil
		}()
		out := &OpenOutput{}
		require.NoError(t, views.open(ctx, &OpenInput{Resource: &identity.ResourceRef{URI: "report://example/summary"}, Parameters: parameters}, out))
		require.NoError(t, <-done)
		command := <-delivered
		require.True(t, out.OK)
		require.Equal(t, "report://example/summary", out.WindowKey)
		require.Equal(t, parameters, out.Parameters)
		require.Equal(t, "report", out.WorkspaceObject.Kind)
		require.Equal(t, "opening", out.WorkspaceObject.Lifecycle.State, "UI acknowledgement is not report-data completion")
		require.Equal(t, "report://example/summary", command["windowKey"])
		expected, _ := json.Marshal(parameters)
		actual, _ := json.Marshal(command["parameters"])
		require.JSONEq(t, string(expected), string(actual))
		options := command["options"].(map[string]any)
		require.Equal(t, "conversation", options["conversationId"])
		ref := options["resourceRef"].(map[string]any)
		require.Equal(t, "report://example/summary", ref["uri"])
		require.NotContains(t, options, "inlineMetadata", "opening a predefined report does not send a designer window")
		postUIRPC(t, bridge, "ui.snapshot", map[string]any{"clientId": "client", "data": map[string]any{"conversationId": "conversation", "clientId": "client", "selected": map[string]any{"windowId": out.WindowID}, "windows": []any{map[string]any{"windowId": out.WindowID, "windowKey": out.WindowKey, "windowTitle": "Summary", "conversationId": "conversation", "parameters": parameters, "viewState": map[string]any{"report": map[string]any{"resourceUri": out.WindowKey, "status": "ready", "parameters": parameters}}}}}})
		windows := windowtool.New(bridge)
		getWindow, _ := windows.Method("get")
		windowOut := &windowtool.GetOutput{}
		require.NoError(t, getWindow(ctx, &windowtool.GetInput{ClientID: "client", WindowID: out.WindowID, Resource: out.Resource}, windowOut))
		require.Equal(t, "report://example/summary", windowOut.Window.Resource.URI)
		state := windowOut.Window.ViewState["report"].(map[string]any)
		require.Equal(t, "ready", state["status"])
		require.Equal(t, "report://example/summary", state["resourceUri"])
		require.NoError(t, getWindow(ctx, &windowtool.GetInput{ClientID: "client", WindowID: out.WindowID}, &windowtool.GetOutput{}))
	}
}

func TestReportResourceOpenRetainsAdmissionAndConversationBoundaries(t *testing.T) {
	views, _, policy, ctx := reportOpenFixture(t)
	policy.allowed = false
	require.Error(t, views.open(ctx, &OpenInput{Resource: &identity.ResourceRef{URI: "report://example/summary"}}, &OpenOutput{}))
	policy.allowed = true
	require.Error(t, views.open(requestctx.WithConversationID(ctx, "other-conversation"), &OpenInput{Resource: &identity.ResourceRef{URI: "report://example/summary"}}, &OpenOutput{}))
	require.Error(t, views.open(ctx, &OpenInput{Resource: &identity.ResourceRef{URI: "report://foreign/summary"}}, &OpenOutput{}))
	require.Error(t, views.open(ctx, &OpenInput{Resource: &identity.ResourceRef{URI: "datasource://example/summary"}}, &OpenOutput{}))
	require.Error(t, views.open(ctx, &OpenInput{ID: "report://example/summary", Resource: &identity.ResourceRef{URI: "report://foreign/summary"}}, &OpenOutput{}))
}

func TestDirectReportCommandRejectsForgedPinAndForeignConversation(t *testing.T) {
	_, bridge, _, ctx := reportOpenFixture(t)
	definition, err := bridge.ReportDefinition(ctx, &forgeuisvc.ReportDefinitionInput{Resource: identity.ResourceRef{URI: "report://example/summary"}})
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		change func(*identity.ResolvedResource)
	}{
		{"changed authority", func(pin *identity.ResolvedResource) { pin.AuthorityBinding = "other-actor" }},
		{"changed candidate", func(pin *identity.ResolvedResource) {
			pin.ContentFingerprint = identity.ContentFingerprint([]byte("changed"))
		}},
		{"expired", func(pin *identity.ResolvedResource) { pin.ValidUntil = time.Now().Add(-time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pin := *definition.Resource
			tc.change(&pin)
			_, err := bridge.UICommand(ctx, &forgeuisvc.UICommandInput{ClientID: "client", Method: "ui.window.open", TimeoutMs: 100, Params: map[string]any{"windowKey": "report://example/summary", "resolvedResource": pin, "options": map[string]any{"conversationId": "conversation"}}})
			require.Error(t, err)
		})
	}
	_, err = bridge.UICommand(ctx, &forgeuisvc.UICommandInput{ClientID: "client", Method: "ui.window.open", Params: map[string]any{"windowKey": "report://example/summary", "options": map[string]any{"conversationId": "other-conversation"}}})
	require.Error(t, err)
	_, err = bridge.UICommand(ctx, &forgeuisvc.UICommandInput{ClientID: "foreign-client", Method: "ui.window.open", Params: map[string]any{"windowKey": "report://example/summary", "options": map[string]any{"conversationId": "conversation"}}})
	require.Error(t, err)
}

func TestReportOpenRejectsAuthorityChangeDuringUIAcknowledgement(t *testing.T) {
	views, bridge, policy, ctx := reportOpenFixture(t)
	done := make(chan struct{})
	go func() {
		response := postUIRPC(t, bridge, "ui.poll", map[string]any{"clientId": "client", "timeoutMs": 2000})
		command, ok := response["params"].(map[string]any)
		if !ok {
			close(done)
			return
		}
		policy.changed = true
		postUIRPC(t, bridge, "ui.response", map[string]any{"id": command["id"], "ok": true, "result": map[string]any{"windowId": "instance"}})
		close(done)
	}()
	out := &OpenOutput{}
	require.Error(t, views.open(ctx, &OpenInput{Resource: &identity.ResourceRef{URI: "report://example/summary"}, TimeoutMs: 2000}, out))
	<-done
	require.Empty(t, out.Items, "a UI acknowledgement must not outlive report admission")
}
