package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/service/browsermcp"
)

type browserThreadStore struct{ aguistore.Store }

func (browserThreadStore) GetThread(_ context.Context, user, thread string) (*aguistore.Thread, error) {
	if user == "owner" && thread == "wire" {
		return &aguistore.Thread{ConversationID: "owned"}, nil
	}
	return nil, browsermcp.ErrUnavailable
}
func TestBrowserMCPCatalogHTTPRequiresOwnedThreadAndAuthenticatedUser(t *testing.T) {
	c := &durableAGUIClient{aguiTestClient: newAGUITestClient(), store: browserThreadStore{}}
	c.owner = "owner"
	registry := browsermcp.New(func(context.Context) ([]cfg.BrowserDescriptor, error) {
		return []cfg.BrowserDescriptor{{Name: "device", ExecutionLocation: "browser", AllowedTools: []string{"read"}}}, nil
	})
	mux := http.NewServeMux()
	registerBrowserMCPRoutes(mux, c, nil, registry)
	input := browsermcp.Registration{ConversationID: "owned", ThreadID: "wire", Server: "device", ConnectionID: "connection1", Tools: []browsermcp.Tool{{Name: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	run := func(user string, value browsermcp.Registration) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(value)
		request := httptest.NewRequest("POST", "/v1/mcp/browser/catalog", strings.NewReader(string(raw)))
		if user != "" {
			request = request.WithContext(iauth.WithUserInfo(request.Context(), &iauth.UserInfo{Subject: user}))
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}
	if result := run("", input); result.Code == 200 {
		t.Fatal("anonymous registration admitted")
	}
	if result := run("owner", input); result.Code != 200 {
		t.Fatal(result.Code, result.Body.String())
	}
	if result := run("other", input); result.Code != 403 {
		t.Fatal("foreign owner admitted", result.Code)
	}
	input.ThreadID = "other-thread"
	if result := run("owner", input); result.Code != 403 {
		t.Fatal("foreign thread admitted", result.Code)
	}
}
func TestBrowserMCPDurableResultRequiresOriginalCatalogProvenance(t *testing.T) {
	metadata := json.RawMessage(`{"browserMCP":{"catalogId":"catalog1","connectionId":"device1","server":"device","tool":"read"}}`)
	pending := aguiPending{ClientTools: []clienttool.PendingCall{{ID: "original", Name: "device-read", Metadata: metadata}}}
	input := &agui.RunAgentInput{ThreadID: "thread", Messages: []agui.Message{{ID: "result", Role: "tool", ToolCallID: "original", Content: json.RawMessage(`"ok"`), Metadata: metadata}}}
	if err := preflightAGUIResume(context.Background(), nil, input, pending); err != nil {
		t.Fatal(err)
	}
	input.Messages[0].Metadata = json.RawMessage(`{"browserMCP":{"catalogId":"catalog1","connectionId":"other"}}`)
	if err := preflightAGUIResume(context.Background(), nil, input, pending); err == nil {
		t.Fatal("connection-substituted result accepted")
	}
}
