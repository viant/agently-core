package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	iauth "github.com/viant/agently-core/internal/auth"
	agrun "github.com/viant/agently-core/pkg/agently/run"
	toolpolicy "github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/streaming"
	api "github.com/viant/agently-core/sdk/api"
	agentsvc "github.com/viant/agently-core/service/agent"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/service/scheduler"
)

func TestHTTPClient_Query(t *testing.T) {
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent/query" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(&agentsvc.QueryOutput{ConversationID: "c1", Content: "ok"})
	}))

	out, err := c.Query(context.Background(), &agentsvc.QueryInput{Query: "hi"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if out == nil || out.Content != "ok" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestHTTPClient_GetConversation(t *testing.T) {
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(&conversation.Conversation{Id: "c1"})
	}))

	out, err := c.GetConversation(context.Background(), "c1")
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if out == nil || out.Id != "c1" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestHTTPClient_GetRun(t *testing.T) {
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs/run-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(&agrun.RunRowsView{
			Id:             "run-1",
			Status:         "running",
			Model:          strPtr("gpt-5.5"),
			ModelProvider:  strPtr("openai"),
			ConversationId: strPtr("conv-1"),
			TurnId:         strPtr("turn-1"),
		})
	}))

	out, err := c.GetRun(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if out == nil || out.Id != "run-1" || out.Status != "running" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestHTTPClient_GetWorkspaceMetadataWithTarget_QueryParams(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(&WorkspaceMetadata{DefaultAgent: "coder"})
	}))

	_, err := c.GetWorkspaceMetadataWithTarget(context.Background(), &MetadataTargetContext{
		Platform:     " web ",
		FormFactor:   " desktop ",
		Surface:      " browser ",
		Capabilities: []string{" markdown ", "", "chart"},
	})
	if err != nil {
		t.Fatalf("GetWorkspaceMetadataWithTarget: %v", err)
	}
	if gotPath != "/v1/workspace/metadata" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotQuery.Get("platform") != "web" || gotQuery.Get("formFactor") != "desktop" || gotQuery.Get("surface") != "browser" {
		t.Fatalf("unexpected target query values: %#v", gotQuery)
	}
	caps := gotQuery["capabilities"]
	if len(caps) != 2 || caps[0] != "markdown" || caps[1] != "chart" {
		t.Fatalf("unexpected capabilities query values: %#v", caps)
	}
}

func TestHTTPClient_GetWorkspaceMetadata_EnvelopeAndDefaultFallbacks(t *testing.T) {
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workspace/metadata" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"defaults": map[string]interface{}{
					"agent":    "coder",
					"model":    "gpt-5.4",
					"embedder": "openai_text",
				},
				"agents": []string{"coder"},
				"models": []string{"gpt-5.4"},
			},
		})
	}))

	out, err := c.GetWorkspaceMetadata(context.Background())
	if err != nil {
		t.Fatalf("GetWorkspaceMetadata: %v", err)
	}
	if out.DefaultAgent != "coder" || out.DefaultModel != "gpt-5.4" || out.DefaultEmbedder != "openai_text" {
		t.Fatalf("default fallbacks were not applied: %#v", out)
	}
	if out.Defaults == nil || out.Defaults.Agent != "coder" || out.Defaults.Model != "gpt-5.4" || out.Defaults.Embedder != "openai_text" {
		t.Fatalf("unexpected defaults payload: %#v", out.Defaults)
	}
	if len(out.Agents) != 1 || out.Agents[0] != "coder" || len(out.Models) != 1 || out.Models[0] != "gpt-5.4" {
		t.Fatalf("unexpected metadata lists: agents=%#v models=%#v", out.Agents, out.Models)
	}
}

func TestHTTPClient_GetForgeWindowMetadataWithTarget_QueryAndEnvelope(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"key": "report/review",
				"view": map[string]interface{}{
					"type": "container",
				},
			},
		})
	}))

	raw, err := c.GetForgeWindowMetadataWithTarget(context.Background(), "report/review", &MetadataTargetContext{
		Platform:     " ios ",
		FormFactor:   " tablet ",
		Surface:      " app ",
		Capabilities: []string{" markdown ", "", "chart"},
	})
	if err != nil {
		t.Fatalf("GetForgeWindowMetadataWithTarget: %v", err)
	}
	if gotPath != "/v1/api/agently/forge/window/report%2Freview" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotQuery.Get("platform") != "ios" || gotQuery.Get("formFactor") != "tablet" || gotQuery.Get("surface") != "app" {
		t.Fatalf("unexpected target query values: %#v", gotQuery)
	}
	caps := gotQuery["capabilities"]
	if len(caps) != 2 || caps[0] != "markdown" || caps[1] != "chart" {
		t.Fatalf("unexpected capabilities query values: %#v", caps)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal raw metadata: %v", err)
	}
	if decoded["key"] != "report/review" {
		t.Fatalf("unexpected unwrapped metadata: %#v", decoded)
	}
	if _, hasData := decoded["data"]; hasData {
		t.Fatalf("expected data envelope to be unwrapped, got %#v", decoded)
	}
}

func TestHTTPClient_GetForgeWindowMetadata_RawPayloadAndRequiredKey(t *testing.T) {
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/api/agently/forge/window/order" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"key": "order"})
	}))

	raw, err := c.GetForgeWindowMetadata(context.Background(), " order ")
	if err != nil {
		t.Fatalf("GetForgeWindowMetadata: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal raw metadata: %v", err)
	}
	if decoded["key"] != "order" {
		t.Fatalf("unexpected raw metadata: %#v", decoded)
	}
	if _, err := c.GetForgeWindowMetadata(context.Background(), " "); err == nil {
		t.Fatalf("expected empty window key to fail")
	}
}

func TestHTTPClient_ListSkillsAndActivateSkill(t *testing.T) {
	var gotPaths []string
	var gotActivateBody map[string]string
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.RequestURI())
		switch {
		case r.URL.Path == "/v1/skills/diagnostics":
			_ = json.NewEncoder(w).Encode(&SkillDiagnosticsOutput{Items: []string{"shadowed demo"}})
		case strings.HasPrefix(r.URL.Path, "/v1/skills/") && strings.HasSuffix(r.URL.Path, "/activate"):
			_ = json.NewDecoder(r.Body).Decode(&gotActivateBody)
			_ = json.NewEncoder(w).Encode(&ActivateSkillOutput{Name: "playwright-cli", Body: "Loaded skill"})
		case r.URL.Path == "/v1/skills":
			_ = json.NewEncoder(w).Encode(&ListSkillsOutput{Items: []SkillItem{{Name: "playwright-cli", Description: "Automate browser"}}})
		default:
			http.NotFound(w, r)
		}
	}))

	listOut, err := c.ListSkills(context.Background(), &ListSkillsInput{ConversationID: "c1"})
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(listOut.Items) != 1 || listOut.Items[0].Name != "playwright-cli" {
		t.Fatalf("unexpected skills output: %#v", listOut)
	}
	actOut, err := c.ActivateSkill(context.Background(), &ActivateSkillInput{ConversationID: "c1", Name: "playwright-cli", Args: "  https://example.com  "})
	if err != nil {
		t.Fatalf("ActivateSkill: %v", err)
	}
	if actOut.Name != "playwright-cli" || !strings.Contains(actOut.Body, "Loaded skill") {
		t.Fatalf("unexpected activate output: %#v", actOut)
	}
	diagOut, err := c.GetSkillDiagnostics(context.Background())
	if err != nil {
		t.Fatalf("GetSkillDiagnostics: %v", err)
	}
	if len(diagOut.Items) != 1 || diagOut.Items[0] != "shadowed demo" {
		t.Fatalf("unexpected diagnostics output: %#v", diagOut)
	}
	if len(gotPaths) != 3 {
		t.Fatalf("got paths = %#v", gotPaths)
	}
	if gotPaths[0] != "/v1/skills?conversationId=c1" {
		t.Fatalf("unexpected list path: %q", gotPaths[0])
	}
	if gotPaths[1] != "/v1/skills/playwright-cli/activate?conversationId=c1" {
		t.Fatalf("unexpected activate path: %q", gotPaths[1])
	}
	if gotActivateBody["args"] != "  https://example.com  " {
		t.Fatalf("ActivateSkill args were not preserved: %#v", gotActivateBody)
	}
	if gotPaths[2] != "/v1/skills/diagnostics" {
		t.Fatalf("unexpected diagnostics path: %q", gotPaths[2])
	}
}

func TestHTTPClient_TemplatesUseFirstClassRoutes(t *testing.T) {
	var gotRequests []string
	var gotInclude string
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequests = append(gotRequests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/v1/templates":
			_ = json.NewEncoder(w).Encode(&ListTemplatesOutput{Items: []TemplateListItem{{
				Name:        "brief",
				Description: "Summary",
				Format:      "markdown",
			}}})
		case "/v1/templates/brief":
			gotInclude = r.URL.Query().Get("includeDocument")
			_ = json.NewEncoder(w).Encode(&GetTemplateOutput{
				Name:             "brief",
				Format:           "markdown",
				Instructions:     "Use bullets",
				IncludedDocument: true,
			})
		default:
			http.NotFound(w, r)
		}
	}))

	listOut, err := c.ListTemplates(context.Background(), &ListTemplatesInput{})
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(listOut.Items) != 1 || listOut.Items[0].Name != "brief" {
		t.Fatalf("unexpected list output: %#v", listOut)
	}
	includeDocument := true
	getOut, err := c.GetTemplate(context.Background(), &GetTemplateInput{Name: "brief", IncludeDocument: &includeDocument})
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if getOut.Name != "brief" || !getOut.IncludedDocument {
		t.Fatalf("unexpected template output: %#v", getOut)
	}
	if strings.Join(gotRequests, ",") != "GET /v1/templates,GET /v1/templates/brief" {
		t.Fatalf("unexpected requests: %#v", gotRequests)
	}
	if gotInclude != "true" {
		t.Fatalf("unexpected includeDocument query: %q", gotInclude)
	}
}

func TestHTTPClient_EncodesSlashBearingTemplateAndSkillSegments(t *testing.T) {
	var gotRequests []string
	var gotActivateBody map[string]string
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequests = append(gotRequests, r.Method+" "+r.URL.RequestURI())
		switch r.URL.Path {
		case "/v1/templates/templates/brief":
			_ = json.NewEncoder(w).Encode(&GetTemplateOutput{Name: "templates/brief", IncludedDocument: true})
		case "/v1/skills/skills/playwright-cli/activate":
			_ = json.NewDecoder(r.Body).Decode(&gotActivateBody)
			_ = json.NewEncoder(w).Encode(&ActivateSkillOutput{Name: "skills/playwright-cli", Body: "Loaded skill"})
		default:
			http.NotFound(w, r)
		}
	}))

	includeDocument := true
	if _, err := c.GetTemplate(context.Background(), &GetTemplateInput{Name: "templates/brief", IncludeDocument: &includeDocument}); err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if _, err := c.ActivateSkill(context.Background(), &ActivateSkillInput{ConversationID: "c1", Name: "skills/playwright-cli", Args: "args"}); err != nil {
		t.Fatalf("ActivateSkill: %v", err)
	}

	if len(gotRequests) != 2 {
		t.Fatalf("got requests = %#v", gotRequests)
	}
	if gotRequests[0] != "GET /v1/templates/templates%2Fbrief?includeDocument=true" {
		t.Fatalf("unexpected template request: %q", gotRequests[0])
	}
	if gotRequests[1] != "POST /v1/skills/skills%2Fplaywright-cli/activate?conversationId=c1" {
		t.Fatalf("unexpected skill request: %q", gotRequests[1])
	}
	if gotActivateBody["args"] != "args" {
		t.Fatalf("unexpected activate body: %#v", gotActivateBody)
	}
}

func TestHTTPClient_WithSessionDebug_SendsDebugHeaders(t *testing.T) {
	var gotHeaders http.Header
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		_ = json.NewEncoder(w).Encode(&WorkspaceMetadata{DefaultAgent: "coder"})
	})

	c, err := NewHTTP("https://sdk.example.test", WithHTTPClient(newHandlerHTTPClient(t, handler)), WithSessionDebug("trace", "conversation", "reactor"))
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	if _, err := c.GetWorkspaceMetadata(context.Background()); err != nil {
		t.Fatalf("GetWorkspaceMetadata: %v", err)
	}
	if gotHeaders.Get(HeaderDebugEnabled) != "true" {
		t.Fatalf("expected %s header", HeaderDebugEnabled)
	}
	if gotHeaders.Get(HeaderDebugLevel) != "trace" {
		t.Fatalf("unexpected %s header: %q", HeaderDebugLevel, gotHeaders.Get(HeaderDebugLevel))
	}
	if gotHeaders.Get(HeaderDebugComponents) != "conversation,reactor" {
		t.Fatalf("unexpected %s header: %q", HeaderDebugComponents, gotHeaders.Get(HeaderDebugComponents))
	}
}

func TestHTTPClient_GetPayloads_UsesBatchEndpoint(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotInput GetPayloadsInput
	var gotRequests int
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequests++
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotInput); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]*conversation.Payload{
			"p1": &conversation.Payload{Id: "p1"},
			"p2": &conversation.Payload{Id: "p2"},
		})
	}))

	out, err := c.GetPayloads(context.Background(), []string{"p1", "p2", "missing", "p1", ""})
	if err != nil {
		t.Fatalf("GetPayloads: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("unexpected payload count: %d", len(out))
	}
	if out["p1"] == nil || out["p1"].Id != "p1" {
		t.Fatalf("missing p1: %#v", out["p1"])
	}
	if out["p2"] == nil || out["p2"].Id != "p2" {
		t.Fatalf("missing p2: %#v", out["p2"])
	}
	if gotRequests != 1 {
		t.Fatalf("expected one request, got %d", gotRequests)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/api/payloads" {
		t.Fatalf("unexpected request: %s %s", gotMethod, gotPath)
	}
	if strings.Join(gotInput.IDs, ",") != "p1,p2,missing" {
		t.Fatalf("unexpected ids: %#v", gotInput.IDs)
	}
}

func TestHTTPClient_UpdateConversation(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody struct {
		Visibility string `json:"visibility"`
		Shareable  *bool  `json:"shareable"`
	}
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err = json.Unmarshal(data, &gotBody); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(&conversation.Conversation{Id: "c1"})
	}))

	shareable := true
	out, err := c.UpdateConversation(context.Background(), &UpdateConversationInput{
		ConversationID: "c1",
		Visibility:     "public",
		Shareable:      &shareable,
	})
	if err != nil {
		t.Fatalf("UpdateConversation: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotPath != "/v1/conversations/c1" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotBody.Visibility != "public" {
		t.Fatalf("unexpected visibility: %q", gotBody.Visibility)
	}
	if gotBody.Shareable == nil || *gotBody.Shareable != true {
		t.Fatalf("unexpected shareable: %#v", gotBody.Shareable)
	}
	if out == nil || out.Id != "c1" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestHTTPClient_DeleteConversation(t *testing.T) {
	var gotMethod string
	var gotPath string
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))

	if err := c.DeleteConversation(context.Background(), "c1"); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotPath != "/v1/conversations/c1" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
}

func TestHTTPClient_ListConversations_QueryParams(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(&ConversationPage{Rows: nil})
	}))

	_, err := c.ListConversations(context.Background(), &ListConversationsInput{
		AgentID:          "agent-1",
		ParentID:         "parent-conv",
		ParentTurnID:     "parent-turn",
		ExcludeScheduled: true,
		Query:            "favorite color",
		Status:           "active",
		Page: &PageInput{
			Limit:     5,
			Cursor:    "c-2",
			Direction: DirectionAfter,
		},
	})
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if gotPath != "/v1/conversations" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotQuery.Get("agentId") != "agent-1" || gotQuery.Get("parentId") != "parent-conv" || gotQuery.Get("parentTurnId") != "parent-turn" || gotQuery.Get("q") != "favorite color" || gotQuery.Get("status") != "active" {
		t.Fatalf("unexpected query values: %#v", gotQuery)
	}
	if gotQuery.Get("excludeScheduled") != "true" {
		t.Fatalf("unexpected excludeScheduled query value: %#v", gotQuery)
	}
	if gotQuery.Get("limit") != "5" || gotQuery.Get("cursor") != "c-2" || gotQuery.Get("direction") != "after" {
		t.Fatalf("unexpected page query values: %#v", gotQuery)
	}
}

func TestHTTPClient_ListLinkedConversations_QueryParams(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(&LinkedConversationPage{Rows: nil})
	}))

	_, err := c.ListLinkedConversations(context.Background(), &ListLinkedConversationsInput{
		ParentConversationID: "parent-conv",
		ParentTurnID:         "parent-turn",
		Page: &PageInput{
			Limit:     3,
			Cursor:    "c-9",
			Direction: DirectionBefore,
		},
	})
	if err != nil {
		t.Fatalf("ListLinkedConversations: %v", err)
	}
	if gotPath != "/v1/conversations/linked" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotQuery.Get("parentConversationId") != "parent-conv" || gotQuery.Get("parentTurnId") != "parent-turn" {
		t.Fatalf("unexpected query values: %#v", gotQuery)
	}
	if gotQuery.Get("limit") != "3" || gotQuery.Get("cursor") != "c-9" || gotQuery.Get("direction") != "before" {
		t.Fatalf("unexpected page query values: %#v", gotQuery)
	}
}

func TestHTTPClient_GetTranscript_QueryParamsAndSelectors(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(&ConversationState{})
	}))

	_, err := c.GetTranscript(context.Background(), &GetTranscriptInput{
		ConversationID:    "c1",
		Since:             "m1",
		IncludeModelCalls: true,
		IncludeToolCalls:  true,
	}, WithTranscriptMessageSelector(&QuerySelector{
		Limit:   1,
		Offset:  2,
		OrderBy: "created_at ASC,id ASC",
	}))
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if gotPath != "/v1/conversations/c1/transcript" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotQuery.Get("since") != "m1" || gotQuery.Get("includeModelCalls") != "true" || gotQuery.Get("includeToolCalls") != "true" {
		t.Fatalf("unexpected query values: %#v", gotQuery)
	}
	rawSelectors := gotQuery.Get("selectors")
	if rawSelectors == "" {
		t.Fatalf("expected selectors query param")
	}
	var selectors map[string]*QuerySelector
	if err := json.Unmarshal([]byte(rawSelectors), &selectors); err != nil {
		t.Fatalf("unmarshal selectors: %v", err)
	}
	if selectors["Message"] == nil {
		t.Fatalf("expected Message selector")
	}
	if selectors["Message"].Limit != 1 || selectors["Message"].Offset != 2 || selectors["Message"].OrderBy != "created_at ASC,id ASC" {
		t.Fatalf("unexpected selector: %#v", selectors["Message"])
	}
}

func TestHTTPClient_StreamEvents_DecodesJSONPayloadType(t *testing.T) {
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/stream" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: model_started\n")
		_, _ = io.WriteString(w, "data:{\"type\":\"model_started\",\"conversationId\":\"c1\",\"streamId\":\"c1\",\"turnId\":\"t1\",\"status\":\"thinking\"}\n\n")
		_, _ = io.WriteString(w, "data:{\"type\":\"assistant\",\"conversationId\":\"c1\",\"streamId\":\"c1\",\"turnId\":\"t1\",\"content\":\"done\"}\n\n")
	}))

	sub, err := c.StreamEvents(context.Background(), &StreamEventsInput{ConversationID: "c1"})
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}
	defer sub.Close()

	var events []*streaming.Event
	timeout := time.After(2 * time.Second)
	for len(events) < 2 {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				t.Fatalf("subscription closed early after %d events", len(events))
			}
			events = append(events, ev)
		case <-timeout:
			t.Fatalf("timed out waiting for events, got %d", len(events))
		}
	}

	if got := events[0].Type; got != streaming.EventTypeModelStarted {
		t.Fatalf("unexpected first event type: %q", got)
	}
	if got := events[0].TurnID; got != "t1" {
		t.Fatalf("unexpected first event turn: %q", got)
	}
	if got := events[1].Type; got != streaming.EventTypeAssistant {
		t.Fatalf("unexpected second event type: %q", got)
	}
	if got := events[1].Content; got != "done" {
		t.Fatalf("unexpected second event content: %q", got)
	}
	// No `finalResponse` on live emissions — the "final message"
	// concept was removed. End-of-turn is signaled separately.
	if events[1].FinalResponse {
		t.Fatalf("expected assistant event to NOT have finalResponse set on a live emission")
	}
}

func TestHandler_Healthz(t *testing.T) {
	handler := NewHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("unexpected health response: %#v", body)
	}
}

type spyQueryClient struct {
	*HTTPClient
	gotInput *agentsvc.QueryInput
}

func (s *spyQueryClient) Query(_ context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
	s.gotInput = input
	return &agentsvc.QueryOutput{ConversationID: "c1", Content: "ok"}, nil
}

type spyToolApprovalClient struct {
	*HTTPClient
	gotListInput   *ListPendingToolApprovalsInput
	gotDecideInput *DecideToolApprovalInput
	decideOutput   *DecideToolApprovalOutput
	decideErr      error
}

func (s *spyToolApprovalClient) ListPendingToolApprovals(_ context.Context, input *ListPendingToolApprovalsInput) (*PendingToolApprovalPage, error) {
	s.gotListInput = input
	return &PendingToolApprovalPage{Rows: []*PendingToolApproval{}, Total: 0, Offset: 0, Limit: 0, HasMore: false}, nil
}

func (s *spyToolApprovalClient) DecideToolApproval(_ context.Context, input *DecideToolApprovalInput) (*DecideToolApprovalOutput, error) {
	s.gotDecideInput = input
	if s.decideErr != nil {
		return nil, s.decideErr
	}
	if s.decideOutput != nil {
		return s.decideOutput, nil
	}
	return &DecideToolApprovalOutput{Status: "ok"}, nil
}

func TestHandler_Query_AssignsAnonymousUserCookie(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyQueryClient{HTTPClient: base}
	handler := NewHandler(spy)

	body := []byte(`{"query":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotInput == nil {
		t.Fatalf("expected Query to be called")
	}
	if spy.gotInput.UserId == "" {
		t.Fatalf("expected anonymous user id to be assigned")
	}
	if got := rec.Result().Cookies(); len(got) == 0 || got[0].Name != anonymousUserCookieName {
		t.Fatalf("expected anonymous user cookie, got %#v", got)
	}
}

type spyMessagesClient struct {
	*HTTPClient
	gotInput *GetMessagesInput
}

func (s *spyMessagesClient) GetMessages(_ context.Context, input *GetMessagesInput) (*MessagePage, error) {
	s.gotInput = input
	return &MessagePage{}, nil
}

func TestHandler_GetMessages_ParsesPageParams(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyMessagesClient{HTTPClient: base}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodGet, "/v1/messages?conversationId=c1&turnId=t1&roles=user,assistant&types=text,tool&limit=3&cursor=m42&direction=before", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotInput == nil {
		t.Fatalf("expected GetMessages to be called")
	}
	if spy.gotInput.ConversationID != "c1" || spy.gotInput.TurnID != "t1" {
		t.Fatalf("unexpected base filters: %#v", spy.gotInput)
	}
	if len(spy.gotInput.Roles) != 2 || spy.gotInput.Roles[0] != "user" || spy.gotInput.Roles[1] != "assistant" {
		t.Fatalf("unexpected roles: %#v", spy.gotInput.Roles)
	}
	if len(spy.gotInput.Types) != 2 || spy.gotInput.Types[0] != "text" || spy.gotInput.Types[1] != "tool" {
		t.Fatalf("unexpected types: %#v", spy.gotInput.Types)
	}
	if spy.gotInput.Page == nil {
		t.Fatalf("expected page input to be parsed")
	}
	if spy.gotInput.Page.Limit != 3 || spy.gotInput.Page.Cursor != "m42" || spy.gotInput.Page.Direction != DirectionBefore {
		t.Fatalf("unexpected page input: %#v", spy.gotInput.Page)
	}
}

func TestHandler_GetMessages_InvalidLimit(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyMessagesClient{HTTPClient: base}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodGet, "/v1/messages?conversationId=c1&limit=abc", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotInput != nil {
		t.Fatalf("GetMessages should not be called on invalid limit")
	}
}

type spyConversationUpdateClient struct {
	*HTTPClient
	gotInput *UpdateConversationInput
	err      error
}

func (s *spyConversationUpdateClient) UpdateConversation(_ context.Context, input *UpdateConversationInput) (*conversation.Conversation, error) {
	s.gotInput = input
	if s.err != nil {
		return nil, s.err
	}
	return &conversation.Conversation{Id: input.ConversationID}, nil
}

type spyConversationDeleteClient struct {
	*HTTPClient
	gotID string
	err   error
}

func (s *spyConversationDeleteClient) DeleteConversation(_ context.Context, id string) error {
	s.gotID = id
	return s.err
}

type spyRunClient struct {
	*HTTPClient
	gotID string
	run   *agrun.RunRowsView
	err   error
}

func (s *spyRunClient) GetRun(_ context.Context, id string) (*agrun.RunRowsView, error) {
	s.gotID = id
	if s.err != nil {
		return nil, s.err
	}
	return s.run, nil
}

func TestHandler_UpdateConversation_ErrorStatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "validation", err: errors.New("conversation ID is required"), wantStatus: http.StatusBadRequest},
		{name: "unsupported", err: errors.New("unsupported visibility"), wantStatus: http.StatusBadRequest},
		{name: "not found", err: errors.New("conversation not found"), wantStatus: http.StatusNotFound},
		{name: "conflict", err: newConflictError("conflict"), wantStatus: http.StatusConflict},
		{name: "internal", err: errors.New("db timeout"), wantStatus: http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, err := NewHTTP("http://127.0.0.1")
			if err != nil {
				t.Fatalf("NewHTTP: %v", err)
			}
			spy := &spyConversationUpdateClient{HTTPClient: base, err: tc.err}
			handler := NewHandler(spy)

			req := httptest.NewRequest(http.MethodPatch, "/v1/conversations/c1", strings.NewReader(`{"title":"x"}`))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestHandler_DeleteConversation(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyConversationDeleteClient{HTTPClient: base}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodDelete, "/v1/conversations/c1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if spy.gotID != "c1" {
		t.Fatalf("unexpected delete id: %q", spy.gotID)
	}
}

func TestHandler_GetRun(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyRunClient{
		HTTPClient: base,
		run: &agrun.RunRowsView{
			Id:             "run-1",
			Status:         "running",
			Model:          strPtr("gpt-5.5"),
			ModelProvider:  strPtr("openai"),
			ConversationId: strPtr("conv-1"),
			TurnId:         strPtr("turn-1"),
		},
	}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodGet, "/v1/runs/run-1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if spy.gotID != "run-1" {
		t.Fatalf("unexpected run id: %q", spy.gotID)
	}
	if !strings.Contains(rec.Body.String(), `"Id":"run-1"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestHandler_DeleteConversation_ErrorStatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "active", err: data.ErrConversationActive, wantStatus: http.StatusConflict},
		{name: "not found", err: data.ErrConversationNotFound, wantStatus: http.StatusNotFound},
		{name: "permission", err: data.ErrPermissionDenied, wantStatus: http.StatusForbidden},
		{name: "validation", err: errors.New("conversation ID is required"), wantStatus: http.StatusBadRequest},
		{name: "internal", err: errors.New("db timeout"), wantStatus: http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, err := NewHTTP("http://127.0.0.1")
			if err != nil {
				t.Fatalf("NewHTTP: %v", err)
			}
			spy := &spyConversationDeleteClient{HTTPClient: base, err: tc.err}
			handler := NewHandler(spy)

			req := httptest.NewRequest(http.MethodDelete, "/v1/conversations/c1", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

type spyQueuedTurnMutationClient struct {
	*HTTPClient
	cancelConversationID string
	cancelTurnID         string
	moveInput            *MoveQueuedTurnInput
	editInput            *EditQueuedTurnInput
}

func (s *spyQueuedTurnMutationClient) CancelQueuedTurn(_ context.Context, conversationID, turnID string) error {
	s.cancelConversationID = conversationID
	s.cancelTurnID = turnID
	return nil
}

func (s *spyQueuedTurnMutationClient) MoveQueuedTurn(_ context.Context, input *MoveQueuedTurnInput) error {
	s.moveInput = input
	return nil
}

func (s *spyQueuedTurnMutationClient) EditQueuedTurn(_ context.Context, input *EditQueuedTurnInput) error {
	s.editInput = input
	return nil
}

func TestHandler_QueuedTurnMutations_ReturnNoContent(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyQueuedTurnMutationClient{HTTPClient: base}
	handler := NewHandler(spy)

	t.Run("delete", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/conversations/c1/turns/t1", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
		}
		if spy.cancelConversationID != "c1" || spy.cancelTurnID != "t1" {
			t.Fatalf("unexpected cancel args: %q %q", spy.cancelConversationID, spy.cancelTurnID)
		}
	})

	t.Run("move", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/conversations/c1/turns/t1/move", strings.NewReader(`{"direction":"up"}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
		}
		if spy.moveInput == nil || spy.moveInput.ConversationID != "c1" || spy.moveInput.TurnID != "t1" || spy.moveInput.Direction != "up" {
			t.Fatalf("unexpected move input: %#v", spy.moveInput)
		}
	})

	t.Run("edit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/v1/conversations/c1/turns/t1", strings.NewReader(`{"content":"edited"}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
		}
		if spy.editInput == nil || spy.editInput.ConversationID != "c1" || spy.editInput.TurnID != "t1" || spy.editInput.Content != "edited" {
			t.Fatalf("unexpected edit input: %#v", spy.editInput)
		}
	})
}

type spyToolResourceClient struct {
	*HTTPClient
	executeName string
	resourceRef *ResourceRef
	saveInput   *SaveResourceInput
	listSkills  *ListSkillsInput
	actSkill    *ActivateSkillInput
}

func (s *spyToolResourceClient) ExecuteTool(_ context.Context, name string, args map[string]interface{}) (string, error) {
	s.executeName = name
	return "ok", nil
}

func (s *spyToolResourceClient) GetResource(_ context.Context, ref *ResourceRef) (*GetResourceOutput, error) {
	s.resourceRef = ref
	return &GetResourceOutput{Kind: ref.Kind, Name: ref.Name, Data: []byte("ok")}, nil
}

func (s *spyToolResourceClient) SaveResource(_ context.Context, input *SaveResourceInput) error {
	s.saveInput = input
	return nil
}

func (s *spyToolResourceClient) DeleteResource(_ context.Context, ref *ResourceRef) error {
	s.resourceRef = ref
	return nil
}

func (s *spyToolResourceClient) ListSkills(_ context.Context, input *ListSkillsInput) (*ListSkillsOutput, error) {
	s.listSkills = input
	return &ListSkillsOutput{Items: []SkillItem{{Name: "playwright-cli"}}}, nil
}

func (s *spyToolResourceClient) ActivateSkill(_ context.Context, input *ActivateSkillInput) (*ActivateSkillOutput, error) {
	s.actSkill = input
	return &ActivateSkillOutput{Name: input.Name, Body: "Loaded skill"}, nil
}

func (s *spyToolResourceClient) GetSkillDiagnostics(_ context.Context) (*SkillDiagnosticsOutput, error) {
	return &SkillDiagnosticsOutput{Items: []string{"shadowed demo"}}, nil
}

func TestHandler_ExecuteTool_EmptyNameIsBadRequest(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyToolResourceClient{HTTPClient: base}
	handler := handleExecuteTool(spy)

	req := httptest.NewRequest(http.MethodPost, "/v1/tools//execute", strings.NewReader(`{}`))
	req.SetPathValue("name", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if spy.executeName != "" {
		t.Fatalf("ExecuteTool should not be called, got %q", spy.executeName)
	}
}

func TestHandler_ResourceHandlers_EmptyPathValuesAreBadRequest(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyToolResourceClient{HTTPClient: base}

	t.Run("get", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/workspace/resources//", nil)
		req.SetPathValue("kind", "")
		req.SetPathValue("name", "")
		rec := httptest.NewRecorder()
		handleGetResource(spy).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})

	t.Run("save", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/v1/workspace/resources//", strings.NewReader("x"))
		req.SetPathValue("kind", "")
		req.SetPathValue("name", "")
		rec := httptest.NewRecorder()
		handleSaveResource(spy).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})

	t.Run("delete", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/workspace/resources//", nil)
		req.SetPathValue("kind", "")
		req.SetPathValue("name", "")
		rec := httptest.NewRecorder()
		handleDeleteResource(spy).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})
}

func TestHandler_SkillHandlers(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyToolResourceClient{HTTPClient: base}

	t.Run("list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/skills?conversationId=c1", nil)
		rec := httptest.NewRecorder()
		handleListSkills(spy).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		if spy.listSkills == nil || spy.listSkills.ConversationID != "c1" {
			t.Fatalf("unexpected list input: %#v", spy.listSkills)
		}
	})

	t.Run("activate", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/skills/playwright-cli/activate?conversationId=c1", strings.NewReader(`{"args":"  https://example.com  "}`))
		req.SetPathValue("name", "playwright-cli")
		rec := httptest.NewRecorder()
		handleActivateSkill(spy).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		if spy.actSkill == nil || spy.actSkill.ConversationID != "c1" || spy.actSkill.Name != "playwright-cli" || spy.actSkill.Args != "  https://example.com  " {
			t.Fatalf("unexpected activate input: %#v", spy.actSkill)
		}
	})

	t.Run("activate rejects malformed json", func(t *testing.T) {
		for _, body := range []string{`{`, `{"args":"ok"} trailing`} {
			req := httptest.NewRequest(http.MethodPost, "/v1/skills/playwright-cli/activate?conversationId=c1", strings.NewReader(body))
			req.SetPathValue("name", "playwright-cli")
			rec := httptest.NewRecorder()
			handleActivateSkill(spy).ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("body %q status = %d, want %d response=%s", body, rec.Code, http.StatusBadRequest, rec.Body.String())
			}
		}
	})

	t.Run("diagnostics", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/skills/diagnostics", nil)
		rec := httptest.NewRecorder()
		handleSkillDiagnostics(spy).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
	})
}

type spyTranscriptClient struct {
	*HTTPClient
	gotInput      *GetTranscriptInput
	gotOptions    []TranscriptOption
	gotPayloadIDs []string
	payloads      map[string]*conversation.Payload
	transcript    *ConversationStateResponse
}

func (s *spyTranscriptClient) GetTranscript(_ context.Context, input *GetTranscriptInput, options ...TranscriptOption) (*ConversationStateResponse, error) {
	s.gotInput = input
	s.gotOptions = options
	if s.transcript != nil {
		return s.transcript, nil
	}
	return &ConversationStateResponse{SchemaVersion: "2", Conversation: &ConversationState{}}, nil
}

func (s *spyTranscriptClient) GetPayloads(_ context.Context, ids []string) (map[string]*conversation.Payload, error) {
	s.gotPayloadIDs = append([]string(nil), ids...)
	out := make(map[string]*conversation.Payload, len(ids))
	for _, id := range ids {
		if s.payloads != nil {
			if payload := s.payloads[id]; payload != nil {
				out[id] = payload
			}
			continue
		}
		out[id] = &conversation.Payload{Id: id}
	}
	return out, nil
}

func (s *spyTranscriptClient) GetLiveState(_ context.Context, conversationID string, options ...TranscriptOption) (*ConversationStateResponse, error) {
	return &ConversationStateResponse{SchemaVersion: "2", Conversation: &ConversationState{ConversationID: conversationID}}, nil
}

func TestHandler_GetTranscript_AcceptsLegacyIncludeToolCallParam(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyTranscriptClient{HTTPClient: base}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodGet, "/v1/conversations/c1/transcript?since=m1&includeModelCall=true&includeToolCall=true", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotInput == nil {
		t.Fatalf("expected GetTranscript to be called")
	}
	if spy.gotInput.Since != "m1" || !spy.gotInput.IncludeModelCalls || !spy.gotInput.IncludeToolCalls {
		t.Fatalf("unexpected transcript input: %#v", spy.gotInput)
	}
}

func TestHandler_GetTranscript_HydratesPersistedInlineReports(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	content := "```forge-data\n" +
		`{"version":2,"scope":"delivery","reportRef":"brief","id":"rows","sequence":1,"format":"json","mode":"replace","data":[{"spend":12}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"scope":"delivery","id":"brief","sequence":2,"mode":"start","title":"Delivery","blocks":[{"id":"summary","kind":"dashboard.summary","dataSourceRef":"rows"}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"scope":"delivery","id":"brief","sequence":3,"mode":"commit"}` +
		"\n```"
	spy := &spyTranscriptClient{
		HTTPClient: base,
		transcript: &ConversationStateResponse{
			SchemaVersion: "2",
			Conversation: &ConversationState{
				ConversationID: "c1",
				Turns: []*TurnState{{
					Messages: []*TurnMessageState{{Role: "assistant", Content: content}},
				}},
			},
		},
	}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodGet, "/v1/conversations/c1/transcript", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var out ConversationStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	message := out.Conversation.Turns[0].Messages[0]
	if message.RenderedContent == nil || len(message.RenderedContent.Reports) != 1 {
		t.Fatalf("expected one hydrated report, got %#v", message.RenderedContent)
	}
	report := message.RenderedContent.Reports[0]
	if report.ID != "brief" || report.Status != "committed" {
		t.Fatalf("unexpected report assembly: %#v", report)
	}
}

func TestHandler_GetPayloads_NormalizesBatchIDs(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyTranscriptClient{HTTPClient: base}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodPost, "/v1/api/payloads", strings.NewReader(`{"ids":["p1"," p2 ","p1",""]}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Join(spy.gotPayloadIDs, ",") != "p1,p2" {
		t.Fatalf("unexpected backend ids: %#v", spy.gotPayloadIDs)
	}
	var out map[string]*conversation.Payload
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out) != 2 || out["p1"] == nil || out["p2"] == nil {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestHandler_GetPayloads_ShapesCompressedInlinePayloads(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	body := []byte(gzipFeedPayload(t, `{"ok":true}`))
	spy := &spyTranscriptClient{
		HTTPClient: base,
		payloads: map[string]*conversation.Payload{
			"p1": &conversation.Payload{Id: "p1", MimeType: "application/json", InlineBody: &body, Compression: "gzip"},
		},
	}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodPost, "/v1/api/payloads", strings.NewReader(`{"ids":["p1"]}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]*conversation.Payload
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	payload := out["p1"]
	if payload == nil || payload.InlineBody == nil {
		t.Fatalf("missing shaped payload: %#v", out)
	}
	if got := string(*payload.InlineBody); got != `{"ok":true}` {
		t.Fatalf("unexpected inline body: %q", got)
	}
	if payload.Compression != "" {
		t.Fatalf("expected cleared compression, got %q", payload.Compression)
	}
}

func TestHandler_GetTranscript_ParsesSelectors(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyTranscriptClient{HTTPClient: base}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodGet, "/v1/conversations/c1/transcript?selectors="+url.QueryEscape(`{"Message":{"limit":1,"offset":2,"orderBy":"created_at ASC,id ASC"}}`), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if len(spy.gotOptions) != 1 {
		t.Fatalf("expected selector option, got %d", len(spy.gotOptions))
	}
	opts := &transcriptOptions{}
	for _, option := range spy.gotOptions {
		option(opts)
	}
	if opts.selectors["Message"] == nil {
		t.Fatalf("expected Message selector")
	}
	if opts.selectors["Message"].Limit != 1 || opts.selectors["Message"].Offset != 2 || opts.selectors["Message"].OrderBy != "created_at ASC,id ASC" {
		t.Fatalf("unexpected selector: %#v", opts.selectors["Message"])
	}
}

type spyExecuteClient struct {
	*HTTPClient
}

func (s *spyExecuteClient) ExecuteTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	if err := toolpolicy.ValidateExecution(ctx, toolpolicy.FromContext(ctx), name, args); err != nil {
		return "", err
	}
	return "ok", nil
}

type templateRouteClient struct {
	*HTTPClient
	listed  bool
	gotName *GetTemplateInput
}

func (s *templateRouteClient) ListTemplates(_ context.Context, _ *ListTemplatesInput) (*ListTemplatesOutput, error) {
	s.listed = true
	return &ListTemplatesOutput{Items: []TemplateListItem{{Name: "brief", Description: "Summary", Format: "markdown"}}}, nil
}

func (s *templateRouteClient) GetTemplate(_ context.Context, input *GetTemplateInput) (*GetTemplateOutput, error) {
	s.gotName = input
	included := input != nil && input.IncludeDocument != nil && *input.IncludeDocument
	return &GetTemplateOutput{Name: input.Name, Format: "markdown", Instructions: "Use bullets", IncludedDocument: included}, nil
}

func TestHandler_TemplatesUseDedicatedRoutesAndClientContract(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &templateRouteClient{HTTPClient: base}
	handler := NewHandler(spy)

	req := httptest.NewRequest(http.MethodGet, "/v1/templates", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected list status: %d body=%s", rec.Code, rec.Body.String())
	}
	var listOut ListTemplatesOutput
	if err := json.NewDecoder(rec.Body).Decode(&listOut); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listOut.Items) != 1 || listOut.Items[0].Name != "brief" {
		t.Fatalf("unexpected list output: %#v", listOut)
	}
	if !spy.listed {
		t.Fatalf("ListTemplates was not called")
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/templates/brief?includeDocument=true", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: %d body=%s", rec.Code, rec.Body.String())
	}
	var getOut GetTemplateOutput
	if err := json.NewDecoder(rec.Body).Decode(&getOut); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if getOut.Name != "brief" || !getOut.IncludedDocument {
		t.Fatalf("unexpected get output: %#v", getOut)
	}
	if spy.gotName == nil || spy.gotName.Name != "brief" || spy.gotName.IncludeDocument == nil || !*spy.gotName.IncludeDocument {
		t.Fatalf("unexpected get input: %#v", spy.gotName)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/templates/templates%2Fbrief?includeDocument=true", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected encoded get status: %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotName == nil || spy.gotName.Name != "templates/brief" {
		t.Fatalf("encoded template path was not decoded as one segment: %#v", spy.gotName)
	}
}

func TestHandler_ExecuteToolByName_DefaultBestPathBlocksRisky(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyExecuteClient{HTTPClient: base}
	handler := NewHandler(spy)

	body := []byte(`{"name":"system/exec:execute","args":{"commands":["date"],"workdir":"/tmp"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_ExecuteToolByName_DefaultBestPathAllowsSafe(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyExecuteClient{HTTPClient: base}
	handler := NewHandler(spy)

	body := []byte(`{"name":"system/os:getEnv","args":{"names":["USER"]}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
}

type upstreamDeniedExecuteClient struct {
	*HTTPClient
}

func (s *upstreamDeniedExecuteClient) ExecuteTool(_ context.Context, _ string, _ map[string]interface{}) (string, error) {
	return "", errors.New(`request failed: 500 Internal Server Error: {"status":"error","message":"user access denied","errors":[{"view":"taxonomy","parameter":"Auth","statusCode":403,"message":"user access denied"}]}`)
}

type partialResultExecuteClient struct {
	*HTTPClient
}

func (s *partialResultExecuteClient) ExecuteTool(_ context.Context, _ string, _ map[string]interface{}) (string, error) {
	return `{"jobId":"job-1","status":"failed","error":"artifact artifact-1 already exists"}`, errors.New("reporting: already exists")
}

func TestHandler_ExecuteToolByName_PreservesUpstreamAuthStatus(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &upstreamDeniedExecuteClient{HTTPClient: base}
	handler := NewHandler(spy)

	body := []byte(`{"name":"platform:Taxonomy","args":{"Name":"travel"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_ExecuteTool_PreservesPartialResultOnError(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &partialResultExecuteClient{HTTPClient: base}
	handler := handleExecuteTool(spy)

	req := httptest.NewRequest(http.MethodPost, "/v1/tools/reporting%3Arun_export/execute", strings.NewReader(`{"jobId":"job-1"}`))
	req.SetPathValue("name", "reporting:run_export")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["error"] != "reporting: already exists" {
		t.Fatalf("unexpected error payload: %#v", payload)
	}
	if payload["result"] == "" {
		t.Fatalf("expected partial result in error payload: %#v", payload)
	}
}

func TestHandler_ExecuteToolByName_PreservesPartialResultOnError(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &partialResultExecuteClient{HTTPClient: base}
	handler := NewHandler(spy)

	body := []byte(`{"name":"reporting:run_export","args":{"jobId":"job-1"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["error"] != "reporting: already exists" {
		t.Fatalf("unexpected error payload: %#v", payload)
	}
	if payload["result"] == "" {
		t.Fatalf("expected partial result in error payload: %#v", payload)
	}
}

type stubSubscription struct {
	id      string
	ch      chan *streaming.Event
	reason  string
	lastSeq int64
}

func (s *stubSubscription) ID() string                 { return s.id }
func (s *stubSubscription) C() <-chan *streaming.Event { return s.ch }
func (s *stubSubscription) Close() error               { return nil }
func (s *stubSubscription) Reason() string             { return s.reason }
func (s *stubSubscription) LastSeq() int64             { return s.lastSeq }

type spyStreamClient struct {
	*HTTPClient
	sub streaming.Subscription
}

func (s *spyStreamClient) StreamEvents(_ context.Context, _ *StreamEventsInput) (streaming.Subscription, error) {
	return s.sub, nil
}

func TestHandler_StreamEvents_EmitsKeepaliveComments(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	sub := &stubSubscription{
		id: "sub-1",
		ch: make(chan *streaming.Event),
	}
	spy := &spyStreamClient{HTTPClient: base, sub: sub}
	handler := NewHandler(spy)

	prevInterval := streamKeepaliveInterval
	streamKeepaliveInterval = 10 * time.Millisecond
	defer func() { streamKeepaliveInterval = prevInterval }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/v1/stream?conversationId=c1", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("stream handler did not exit after context cancellation")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !strings.Contains(got, ": keepalive\n\n") {
		t.Fatalf("expected keepalive comment in SSE stream, got %q", got)
	}
}

// TestHandler_StreamEvents_EmitsOverflowTerminalEvent verifies the Phase-1
// backpressure wiring: when the bus closes a subscription because the
// subscriber's buffer filled, the SSE handler must emit an explicit
// `stream_overflow` event carrying the last delivered EventSeq so the UI
// knows to reconnect rather than assuming a clean end-of-stream.
func TestHandler_StreamEvents_EmitsOverflowTerminalEvent(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	ch := make(chan *streaming.Event, 1)
	ch <- &streaming.Event{Type: streaming.EventTypeTextDelta, EventSeq: 7, ConversationID: "c1"}
	close(ch)
	sub := &stubSubscription{
		id:      "sub-1",
		ch:      ch,
		reason:  streaming.ReasonOverflow,
		lastSeq: 7,
	}
	spy := &spyStreamClient{HTTPClient: base, sub: sub}
	handler := NewHandler(spy)

	prevInterval := streamKeepaliveInterval
	streamKeepaliveInterval = time.Hour // keep keepalive out of the way
	defer func() { streamKeepaliveInterval = prevInterval }()

	req := httptest.NewRequest(http.MethodGet, "/v1/stream?conversationId=c1", nil)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler did not exit after channel close")
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"type":"stream_overflow"`) {
		t.Fatalf("expected stream_overflow terminal event, got %q", body)
	}
	if !strings.Contains(body, `"eventSeq":7`) {
		t.Fatalf("expected overflow event to carry eventSeq=7, got %q", body)
	}
	if !strings.Contains(body, `"status":"overflow"`) {
		t.Fatalf("expected status=overflow, got %q", body)
	}
}

func TestHTTPClient_GetSchedule(t *testing.T) {
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/v1/api/agently/scheduler/schedule/sched-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"data":   &scheduler.Schedule{ID: "sched-1", Name: "daily-report"},
		})
	}))

	out, err := c.GetSchedule(context.Background(), "sched-1")
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if out == nil || out.ID != "sched-1" || out.Name != "daily-report" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestHTTPClient_ListSchedules(t *testing.T) {
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/v1/api/agently/scheduler/" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"data": map[string]interface{}{
				"schedules": []*scheduler.Schedule{
					{ID: "s1", Name: "first"},
					{ID: "s2", Name: "second"},
				},
			},
		})
	}))

	out, err := c.ListSchedules(context.Background())
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}
	if len(out) != 2 || out[0].ID != "s1" || out[1].ID != "s2" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestHTTPClient_UpsertSchedules(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody struct {
		Schedules []*scheduler.Schedule `json:"schedules"`
	}
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err = json.Unmarshal(data, &gotBody); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	err := c.UpsertSchedules(context.Background(), []*scheduler.Schedule{
		{ID: "s1", Name: "first", Enabled: true},
	})
	if err != nil {
		t.Fatalf("UpsertSchedules: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotPath != "/v1/api/agently/scheduler/" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if len(gotBody.Schedules) != 1 || gotBody.Schedules[0].ID != "s1" {
		t.Fatalf("unexpected body: %#v", gotBody)
	}
}

func TestHTTPClient_RunScheduleNow(t *testing.T) {
	var gotMethod string
	var gotPath string
	c := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))

	err := c.RunScheduleNow(context.Background(), "sched-1")
	if err != nil {
		t.Fatalf("RunScheduleNow: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotPath != "/v1/api/agently/scheduler/run-now/sched-1" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
}

func TestResolveQueryUserID_AuthDisabled_AssignsAnonymousCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", nil)

	got := resolveQueryUserID(rec, req, "", nil)
	if got == "" {
		t.Fatal("expected anonymous user id, got empty")
	}
	if !strings.HasPrefix(got, "anonymous:") {
		t.Fatalf("expected anonymous: prefix, got %q", got)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != anonymousUserCookieName {
		t.Fatalf("expected anonymous cookie, got %#v", cookies)
	}
	if !cookies[0].Secure {
		t.Fatalf("expected anonymous cookie to be Secure")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestHTTPClient_DownloadFile_RejectsInformationalStatus(t *testing.T) {
	c, err := NewHTTP("http://example.invalid")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	c.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 199,
			Status:     "199 Informational",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("informational")),
			Request:    req,
		}, nil
	})
	_, err = c.DownloadFile(context.Background(), &DownloadFileInput{
		ConversationID: "conv-1",
		FileID:         "file-1",
	})
	if err == nil {
		t.Fatalf("expected error for informational response")
	}
	if !strings.Contains(err.Error(), "download file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveQueryUserID_AuthEnabled_ReturnsEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", nil)
	cfg := &svcauth.Config{Enabled: true}

	got := resolveQueryUserID(rec, req, "", cfg)
	if got != "" {
		t.Fatalf("expected empty user id when auth enabled, got %q", got)
	}
	if cookies := rec.Result().Cookies(); len(cookies) > 0 {
		t.Fatalf("expected no cookies when auth enabled, got %#v", cookies)
	}
}

func TestResolveQueryUserID_AuthEnabled_UsesContextUser(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", nil)
	ctx := iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: "oauth-user-42"})
	req = req.WithContext(ctx)
	cfg := &svcauth.Config{Enabled: true}

	got := resolveQueryUserID(rec, req, "", cfg)
	if got != "oauth-user-42" {
		t.Fatalf("expected context user, got %q", got)
	}
}

func TestResolveQueryUserID_ExplicitUserAlwaysWins(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", nil)
	ctx := iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: "ctx-user"})
	req = req.WithContext(ctx)
	cfg := &svcauth.Config{Enabled: true}

	got := resolveQueryUserID(rec, req, "explicit-user", cfg)
	if got != "explicit-user" {
		t.Fatalf("expected explicit user, got %q", got)
	}
}

func TestHandler_Query_Returns401_WhenAuthEnabledAndNoUser(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyQueryClient{HTTPClient: base}
	sessions := svcauth.NewManager(time.Hour, nil)
	handler, err := NewHandlerWithContext(
		context.Background(),
		spy,
		WithAuth(&svcauth.Config{Enabled: true, IpHashKey: "test-key", CookieName: "sess"}, sessions),
	)
	if err != nil {
		t.Fatalf("NewHandlerWithContext: %v", err)
	}

	body := []byte(`{"query":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotInput != nil {
		t.Fatal("expected Query NOT to be called when unauthorized")
	}
}

func TestHandler_Query_Succeeds_WhenAuthEnabledAndUserInContext(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyQueryClient{HTTPClient: base}
	authCfg := &svcauth.Config{
		Enabled:    true,
		IpHashKey:  "test-key",
		CookieName: "sess",
		Local:      &svcauth.Local{Enabled: true},
	}
	sessions := svcauth.NewManager(time.Hour, nil)

	// Create a session so the Protect middleware can find it.
	sess := &svcauth.Session{
		ID:        "test-session",
		Username:  "testuser",
		Subject:   "testuser",
		CreatedAt: time.Now(),
	}
	sessions.Put(context.Background(), sess)

	handler, err := NewHandlerWithContext(
		context.Background(),
		spy,
		WithAuth(authCfg, sessions),
	)
	if err != nil {
		t.Fatalf("NewHandlerWithContext: %v", err)
	}

	body := []byte(`{"query":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/query", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "sess", Value: "test-session"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotInput == nil {
		t.Fatal("expected Query to be called")
	}
	if spy.gotInput.UserId != "testuser" {
		t.Fatalf("expected userId=testuser, got %q", spy.gotInput.UserId)
	}
}

func TestHandler_ListPendingToolApprovals_UsesContextUserScope(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyToolApprovalClient{HTTPClient: base}
	authCfg := &svcauth.Config{
		Enabled:    true,
		IpHashKey:  "test-key",
		CookieName: "sess",
		Local:      &svcauth.Local{Enabled: true},
	}
	sessions := svcauth.NewManager(time.Hour, nil)
	sessions.Put(context.Background(), &svcauth.Session{
		ID:        "test-session",
		Username:  "testuser",
		Subject:   "testuser",
		CreatedAt: time.Now(),
	})
	handler, err := NewHandlerWithContext(context.Background(), spy, WithAuth(authCfg, sessions))
	if err != nil {
		t.Fatalf("NewHandlerWithContext: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/tool-approvals/pending?userId=other-user&status=pending", nil)
	req.AddCookie(&http.Cookie{Name: "sess", Value: "test-session"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for mismatched query user, got %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotListInput != nil {
		t.Fatal("expected ListPendingToolApprovals NOT to be called on mismatched user scope")
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/tool-approvals/pending?status=pending", nil)
	req.AddCookie(&http.Cookie{Name: "sess", Value: "test-session"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotListInput == nil {
		t.Fatal("expected ListPendingToolApprovals to be called")
	}
	if spy.gotListInput.UserID != "testuser" {
		t.Fatalf("expected userId=testuser, got %q", spy.gotListInput.UserID)
	}
}

// TestHandler_ListPendingToolApprovals_ForwardsOutcomeSinceCursor pins
// the HTTP transport contract for the durable outcome cursor: the
// handler must read the opaque ?outcomeSince=<cursor> query
// parameter and pass it through verbatim on the canonical
// ListPendingToolApprovalsInput so the backend can re-emit outcomes
// that the client missed between polls. Without this plumbing the
// durability contract proved at the SDK layer would not reach
// over-the-wire callers.
func TestHandler_ListPendingToolApprovals_ForwardsOutcomeSinceCursor(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyToolApprovalClient{HTTPClient: base}
	authCfg := &svcauth.Config{
		Enabled:    true,
		IpHashKey:  "test-key",
		CookieName: "sess",
		Local:      &svcauth.Local{Enabled: true},
	}
	sessions := svcauth.NewManager(time.Hour, nil)
	sessions.Put(context.Background(), &svcauth.Session{
		ID:        "test-session",
		Username:  "testuser",
		Subject:   "testuser",
		CreatedAt: time.Now(),
	})
	handler, err := NewHandlerWithContext(context.Background(), spy, WithAuth(authCfg, sessions))
	if err != nil {
		t.Fatalf("NewHandlerWithContext: %v", err)
	}

	cursor := "2026-05-26T12:00:00.000000000Z"
	req := httptest.NewRequest(http.MethodGet, "/v1/tool-approvals/pending?status=pending&outcomeSince="+url.QueryEscape(cursor), nil)
	req.AddCookie(&http.Cookie{Name: "sess", Value: "test-session"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotListInput == nil {
		t.Fatal("expected ListPendingToolApprovals to be called")
	}
	if spy.gotListInput.OutcomeSince != cursor {
		t.Fatalf("expected OutcomeSince=%q to flow through, got %q", cursor, spy.gotListInput.OutcomeSince)
	}
}

func TestHandler_DecideToolApproval_UsesContextUserScope(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyToolApprovalClient{HTTPClient: base}
	authCfg := &svcauth.Config{
		Enabled:    true,
		IpHashKey:  "test-key",
		CookieName: "sess",
		Local:      &svcauth.Local{Enabled: true},
	}
	sessions := svcauth.NewManager(time.Hour, nil)
	sessions.Put(context.Background(), &svcauth.Session{
		ID:        "test-session",
		Username:  "testuser",
		Subject:   "testuser",
		CreatedAt: time.Now(),
	})
	handler, err := NewHandlerWithContext(context.Background(), spy, WithAuth(authCfg, sessions))
	if err != nil {
		t.Fatalf("NewHandlerWithContext: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/tool-approvals/approval-1/decision", bytes.NewReader([]byte(`{"action":"approve","userId":"other-user"}`)))
	req.AddCookie(&http.Cookie{Name: "sess", Value: "test-session"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for mismatched body user, got %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotDecideInput != nil {
		t.Fatal("expected DecideToolApproval NOT to be called on mismatched user scope")
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/tool-approvals/approval-1/decision", bytes.NewReader([]byte(`{"action":"approve"}`)))
	req.AddCookie(&http.Cookie{Name: "sess", Value: "test-session"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.gotDecideInput == nil {
		t.Fatal("expected DecideToolApproval to be called")
	}
	if spy.gotDecideInput.UserID != "testuser" {
		t.Fatalf("expected userId=testuser, got %q", spy.gotDecideInput.UserID)
	}
}

func TestHandler_DecideToolApproval_ReturnsConflictWhenApprovedExecutionFails(t *testing.T) {
	base, err := NewHTTP("http://127.0.0.1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	spy := &spyToolApprovalClient{
		HTTPClient: base,
		decideOutput: &DecideToolApprovalOutput{
			Status: "ok",
			Outcome: &api.DecideToolApprovalOutcome{
				ApprovalID:   "approval-1",
				Action:       "approve",
				Status:       "failed",
				ErrorMessage: "platform patch failed",
			},
		},
	}
	handler, err := NewHandlerWithContext(context.Background(), spy)
	if err != nil {
		t.Fatalf("NewHandlerWithContext: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/tool-approvals/approval-1/decision", bytes.NewReader([]byte(`{"action":"approve","userId":"devuser"}`)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "platform patch failed") {
		t.Fatalf("expected error body to include tool error, got %s", rec.Body.String())
	}
	if spy.gotDecideInput == nil {
		t.Fatal("expected DecideToolApproval to be called")
	}
}
