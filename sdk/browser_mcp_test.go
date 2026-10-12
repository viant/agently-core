package sdk

import (
	"context"
	"encoding/json"
	"errors"
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

func TestBrowserMCPGetCatalogProjectsOnlyClosedFailureReasons(t *testing.T) {
	for _, mode := range []string{"ready", "catalogMissing", "catalogOwnerMismatch", "catalogConfigUnavailable", "catalogConfigChanged", "conversationUnavailable", "conversationOwnerMismatch"} {
		t.Run(mode, func(t *testing.T) {
			c := newAGUITestClient()
			c.owner = "PRIVATE-owner"
			descriptor := cfg.BrowserDescriptor{Name: "device", ExecutionLocation: "browser", AllowedTools: []string{"read"}}
			var providerErr error
			registry := browsermcp.New(func(context.Context) ([]cfg.BrowserDescriptor, error) {
				return []cfg.BrowserDescriptor{descriptor}, providerErr
			})
			catalog, e := registry.Register(context.Background(), "PRIVATE-owner", browsermcp.Registration{ConversationID: "PRIVATE-conversation", ThreadID: "PRIVATE-thread", Server: "device", ConnectionID: "PRIVATE-connection", Tools: []browsermcp.Tool{{Name: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
			if e != nil {
				t.Fatal(e)
			}
			user, id := "PRIVATE-owner", catalog.ID
			switch mode {
			case "catalogMissing":
				id = "PRIVATE-missing"
			case "catalogOwnerMismatch":
				user = "PRIVATE-other"
			case "catalogConfigUnavailable":
				providerErr = errors.New("PRIVATE provider error")
			case "catalogConfigChanged":
				descriptor.AllowedTools = []string{"changed"}
			case "conversationUnavailable":
				c.conversationErr = errors.New("PRIVATE conversation error")
			case "conversationOwnerMismatch":
				c.owner = "PRIVATE-other"
			}
			mux := http.NewServeMux()
			registerBrowserMCPRoutes(mux, c, nil, registry)
			req := httptest.NewRequest("GET", "/v1/mcp/browser/catalog/"+id, nil)
			req = req.WithContext(iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: user}))
			reply := httptest.NewRecorder()
			mux.ServeHTTP(reply, req)
			if mode == "ready" {
				if reply.Code != 200 {
					t.Fatal(reply.Code, reply.Body.String())
				}
				return
			}
			var body map[string]string
			if json.Unmarshal(reply.Body.Bytes(), &body) != nil || reply.Code != 403 || len(body) != 1 || body["error"] != browsermcp.ErrUnavailable.Error()+" ("+mode+")" || strings.Contains(reply.Body.String(), "PRIVATE") {
				t.Fatal(reply.Code, reply.Body.String())
			}
		})
	}
}

func TestBrowserMCPContinuationReturnsClosedValidationReasonBeforeResume(t *testing.T) {
	for _, mode := range []string{"catalogMetadataMissing", "catalogThreadMismatch", "catalogDescriptionChanged", "catalogSchemaChanged", "catalogMetadataChanged", "catalogMissing"} {
		t.Run(mode, func(t *testing.T) {
			c := &durableAGUIClient{aguiTestClient: newAGUITestClient(), store: browserThreadStore{}}
			c.owner = "owner"
			registry := browsermcp.New(func(context.Context) ([]cfg.BrowserDescriptor, error) {
				return []cfg.BrowserDescriptor{{Name: "device", ExecutionLocation: "browser", AllowedTools: []string{"read"}}}, nil
			})
			catalog, e := registry.Register(context.Background(), "owner", browsermcp.Registration{ConversationID: "owned", ThreadID: "wire", Server: "device", ConnectionID: "PRIVATE-connection", Tools: []browsermcp.Tool{{Name: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
			if e != nil {
				t.Fatal(e)
			}
			input := agui.RunAgentInput{ThreadID: "wire", RunID: "continuation", Tools: catalog.Tools, Messages: []agui.Message{{ID: "result", Role: "tool", ToolCallID: "original", Content: json.RawMessage(`"done"`), Metadata: catalog.Tools[0].Metadata}}}
			switch mode {
			case "catalogMetadataMissing":
				input.Tools[0].Metadata = nil
			case "catalogThreadMismatch":
				input.ThreadID = "PRIVATE-other"
			case "catalogDescriptionChanged":
				input.Tools[0].Description = "PRIVATE-modified"
			case "catalogSchemaChanged":
				input.Tools[0].Parameters = json.RawMessage(`{"type":"object","properties":{"PRIVATE":{"type":"string"}}}`)
			case "catalogMetadataChanged":
				var m map[string]any
				json.Unmarshal(input.Tools[0].Metadata, &m)
				m["extra"] = "PRIVATE-value"
				input.Tools[0].Metadata, _ = json.Marshal(m)
			case "catalogMissing":
				if e = registry.Revoke("owner", catalog.ID); e != nil {
					t.Fatal(e)
				}
			}
			raw, _ := json.Marshal(input)
			request := httptest.NewRequest("POST", "/v1/agui/run", strings.NewReader(string(raw)))
			request = request.WithContext(iauth.WithUserInfo(request.Context(), &iauth.UserInfo{Subject: "owner"}))
			response := httptest.NewRecorder()
			handleAGUIDurable(c, c, nil, AGUIWorkspaceBindings{BrowserMCP: registry})(response, request)
			var body map[string]string
			if json.Unmarshal(response.Body.Bytes(), &body) != nil || response.Code != 403 || body["error"] != browsermcp.ErrUnavailable.Error()+" ("+mode+")" || strings.Contains(response.Body.String(), "PRIVATE") {
				t.Fatal(response.Code, response.Body.String())
			}
			if c.queries.Load() != 0 || c.resumes.Load() != 0 || c.completions.Load() != 0 {
				t.Fatal("invalid continuation executed")
			}
		})
	}
}
