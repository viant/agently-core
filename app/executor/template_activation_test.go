package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	base "github.com/viant/agently-core/genai/llm/provider/base"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/tool"
	templatesvc "github.com/viant/agently-core/protocol/tool/service/template"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/core/modelcall"
	"github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	primitive "github.com/viant/forge/backend/provider/contract"
	identity "github.com/viant/forge/backend/resource"
)

type templateActivationSource struct {
	cohortMetadataFixture
	state    primitive.ResourceState
	provider string
}

func (s *templateActivationSource) CurrentHash(_ context.Context, uri string) (string, error) {
	if uri != s.state.URI {
		return "", identity.ErrResourceDenied
	}
	return s.state.ContentFingerprint, nil
}
func (s *templateActivationSource) Namespaces(context.Context) ([]primitive.NamespaceCapabilities, error) {
	return []primitive.NamespaceCapabilities{{ProviderIdentity: s.provider, Namespace: s.state.Namespace, Kinds: []primitive.KindSupport{{Kind: "template", Operations: []string{"list", "get"}}}}}, nil
}
func (s *templateActivationSource) List(_ context.Context, kind, namespace string) ([]primitive.ResourceState, error) {
	if (kind == "" || kind == "template") && (namespace == "" || namespace == s.state.Namespace) {
		return []primitive.ResourceState{s.state}, nil
	}
	return nil, nil
}
func (s *templateActivationSource) Get(_ context.Context, uri string) (*primitive.ResourceState, error) {
	if uri != s.state.URI {
		return nil, identity.ErrResourceDenied
	}
	result := s.state
	return &result, nil
}

type templateActivationModel struct {
	requests [][]llm.Message
	selected string
}

func (m *templateActivationModel) Generate(ctx context.Context, in *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	observer := modelcall.ObserverFromContext(ctx)
	info := modelcall.Info{Provider: "synthetic", Model: "synthetic", ModelKind: "chat", LLMRequest: in, StartedAt: time.Now()}
	if observer != nil {
		var err error
		ctx, err = observer.OnCallStart(ctx, info)
		if err != nil {
			return nil, err
		}
	}
	m.requests = append(m.requests, append([]llm.Message(nil), in.Messages...))
	message := llm.Message{Role: llm.RoleAssistant, Content: "Fixture complete."}
	if len(m.requests) == 1 {
		message.Content = ""
		message.ToolCalls = []llm.ToolCall{{ID: "activate-template", Name: "template:get", Arguments: map[string]interface{}{"name": m.selected, "includeDocument": true}}}
	}
	response := &llm.GenerateResponse{ResponseID: fmt.Sprintf("fixture-response-%d", len(m.requests)), Choices: []llm.Choice{{Message: message}}}
	if observer != nil {
		info.LLMResponse, info.CompletedAt = response, time.Now()
		if err := observer.OnCallEnd(ctx, info); err != nil {
			return nil, err
		}
	}
	return response, nil
}
func (*templateActivationModel) Implements(feature string) bool { return feature == base.CanUseTools }

type templateActivationAgentFinder struct{ agent *agentmdl.Agent }

func (f templateActivationAgentFinder) Find(context.Context, string) (*agentmdl.Agent, error) {
	return f.agent, nil
}

type templateActivationModelFinder struct{ model *templateActivationModel }

func (f templateActivationModelFinder) Find(context.Context, string) (llm.Model, error) {
	return f.model, nil
}

func TestPublishedTemplateActivationInModelRequest(t *testing.T) {
	const north = "template://north/brief"
	const south = "template://south/brief"
	const northBody = "North synthetic template activation contract."
	const southBody = "South synthetic template activation contract."
	for _, scenario := range []struct {
		name, selected, activate string
		northFirst, southSecond  int
		persisted                bool
	}{
		{name: "preselected", selected: north, activate: north, northFirst: 1},
		{name: "explicit", activate: south, southSecond: 1, persisted: true},
		{name: "same basename across providers", selected: north, activate: south, northFirst: 1, southSecond: 1, persisted: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			old := workspace.Root()
			workspace.SetRoot(root)
			t.Cleanup(func() { workspace.SetRoot(old) })
			t.Setenv("AGENTLY_WORKSPACE", root)
			t.Setenv("AGENTLY_RUNTIME_ROOT", root)
			t.Setenv("AGENTLY_DB_DRIVER", "sqlite")
			t.Setenv("AGENTLY_DB_DSN", "")
			t.Setenv("AGENTLY_DB_PATH", filepath.Join(root, "runtime.db"))
			t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
			ctx := context.Background()
			store := fsstore.New(root)
			require.NoError(t, store.Save(ctx, "", "config", []byte("ui: {}\n")))
			ag := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "reader"}, ModelSelection: llm.ModelSelection{Model: "synthetic"}, Prompt: &binding.Prompt{Engine: "go", Text: "{{.Task.Prompt}}"}, Tool: agentmdl.Tool{Items: []*llm.Tool{{Name: "template:get", AlwaysInclude: true}}}}
			model := &templateActivationModel{selected: scenario.activate}
			finder := templateActivationAgentFinder{agent: ag}
			verified := identity.VerifiedActor{Issuer: "https://identity.example.test", Subject: "reader", TenantID: "tenant", AccountID: "account", IdentityRevision: "one", ValidUntil: time.Now().Add(time.Hour)}
			actor := func(context.Context) (identity.VerifiedActor, error) { return verified, nil }
			verify := func(context.Context, identity.VerifiedActor) error { return nil }
			runtime, err := NewBuilder().WithStore(store).WithKnowledgeStore(fsstore.NewKnowledgeStore(root)).WithStateStore(fsstore.NewStateStore(root)).WithModelFinder(templateActivationModelFinder{model}).WithAgentFinder(finder).WithPrimitiveAuthority(actor, verify, "local-workspace").WithPrimitiveGatewayIdentity("host").WithSkipRegistryInitialize(true).Build(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
			for _, item := range []struct{ ns, body string }{{"north", northBody}, {"south", southBody}} {
				uri := "template://" + item.ns + "/brief"
				provider := item.ns + "-publisher"
				raw, err := json.Marshal(map[string]any{"schemaVersion": 1, "template": map[string]any{"id": "brief", "name": "Brief", "instructions": item.body + "\n" + strings.Repeat("Additional synthetic format instructions. ", 1000), "schema": map[string]any{"type": "object", "title": item.ns + " schema"}}})
				require.NoError(t, err)
				state := primitive.ResourceState{URI: uri, Kind: "template", Namespace: item.ns, Name: "brief", FormatVersion: 1, DefinitionBytes: raw, ContentFingerprint: identity.ContentFingerprint(raw)}
				require.NoError(t, runtime.InstallPublishedMetadata(resource.Connection{Name: provider, ProviderIdentity: provider}, []string{item.ns}, &templateActivationSource{state: state, provider: provider}))
			}
			templateService := templatesvc.New(tplrepo.NewWithReader(resource.NewTemplateReader(runtime.PrimitiveProviders)), nil, templatesvc.WithConversationClient(runtime.Conversation), templatesvc.WithAgentFinder(finder))
			require.NoError(t, tool.AddInternalService(runtime.Registry, templateService))
			input := &agentsvc.QueryInput{ConversationID: "template-conversation", AgentID: ag.ID, Agent: ag, TemplateId: scenario.selected, UserId: "reader", Query: "Use the fixture template."}
			require.NoError(t, runtime.Agent.Query(ctx, input, &agentsvc.QueryOutput{}))
			require.Len(t, model.requests, 2)
			messages := func(req []llm.Message) string {
				var out strings.Builder
				for _, msg := range req {
					out.WriteString(msg.Content)
					out.WriteByte('\n')
				}
				return out.String()
			}
			first := messages(model.requests[0])
			second := messages(model.requests[1])
			require.Equal(t, scenario.northFirst, strings.Count(first, northBody))
			require.Zero(t, strings.Count(first, southBody))
			require.Equal(t, scenario.northFirst, strings.Count(second, northBody))
			require.Equal(t, scenario.northFirst, strings.Count(second, "Active template: "+north))
			require.Equal(t, scenario.southSecond, strings.Count(second, southBody))
			require.Equal(t, scenario.southSecond, strings.Count(second, "Active template: "+south))
			var receipt struct {
				URI              string `json:"uri"`
				MessageID        string `json:"messageId"`
				Instructions     string `json:"instructions"`
				IncludedDocument bool   `json:"includedDocument"`
			}
			for _, msg := range model.requests[1] {
				if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "includedDocument") {
					require.NoError(t, json.Unmarshal([]byte(msg.Content), &receipt))
					require.Less(t, len(msg.Content), 250)
				}
			}
			require.True(t, receipt.IncludedDocument)
			require.Equal(t, scenario.activate, receipt.URI)
			require.Empty(t, receipt.Instructions)
			if scenario.persisted {
				require.NotEmpty(t, receipt.MessageID)
				require.Contains(t, second, "south schema")
				stored, err := runtime.Conversation.GetMessage(ctx, receipt.MessageID, apiconv.WithIncludeToolCall(false))
				require.NoError(t, err)
				require.NotNil(t, stored)
				require.Contains(t, stored.GetContent(), southBody)
			} else {
				require.Empty(t, receipt.MessageID, "a preselected document needs no duplicate persistence")
			}
			later := &agentsvc.QueryInput{ConversationID: input.ConversationID, AgentID: ag.ID, Agent: ag, UserId: "reader", Query: "New unrelated turn."}
			require.NoError(t, runtime.Agent.Query(ctx, later, &agentsvc.QueryOutput{}))
			require.Len(t, model.requests, 3)
			require.NotContains(t, messages(model.requests[2]), northBody)
			require.NotContains(t, messages(model.requests[2]), southBody)
		})
	}
}
