package core

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/openai"
	iauth "github.com/viant/agently-core/internal/auth"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/agently-core/protocol/binding"
	resource "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"testing"
	"time"
)

type contractAnchorClient struct {
	apiconv.Client
	request      []byte
	anchorID     string
	conversation *apiconv.Conversation
}

func (c *contractAnchorClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	trace, payload := c.anchorID, "anchor-request"
	if trace == "" {
		trace = "response-anchor"
	}
	if c.conversation != nil {
		return c.conversation, nil
	}
	out := &apiconv.Conversation{}
	out.Id = "contract-conversation"
	out.CreatedByUserId = ptrContract("owner")
	out.Transcript = []*conversationmodel.TranscriptView{{Message: []*conversationmodel.MessageView{{ModelCall: &conversationmodel.ModelCallView{TraceId: &trace, RequestPayloadId: &payload}}}}}
	return out, nil
}
func (c *contractAnchorClient) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	b := append([]byte(nil), c.request...)
	return &apiconv.Payload{InlineBody: &b, Compression: "none"}, nil
}
func ptrContract(s string) *string { return &s }
func TestContinuationRejectsChangedAgentContract(t *testing.T) {
	previous := &llm.GenerateRequest{Instructions: "AGENT_A", Messages: []llm.Message{{Role: llm.RoleSystem, Content: "AGENT_A"}}, Options: &llm.Options{Model: "gpt-4o-mini", Metadata: map[string]any{"agentId": "agent-a"}}}
	raw, err := json.Marshal(previous)
	require.NoError(t, err)
	service := &Service{convClient: &contractAnchorClient{request: raw}}
	ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
	ctx = requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "contract-conversation"})
	now := time.Now()
	history := &binding.History{LastResponse: &binding.Trace{ID: "response-anchor", At: now}, Traces: map[string]*binding.Trace{binding.ContentMessageKey("new-user"): {At: now.Add(time.Second)}}}
	current := &llm.GenerateRequest{Instructions: "AGENT_B", Messages: []llm.Message{{Role: llm.RoleSystem, Content: "AGENT_B"}, {ID: "new-user", Role: llm.RoleUser, Content: "new task"}}, Options: &llm.Options{Model: "gpt-4o-mini", Metadata: map[string]any{"agentId": "agent-b"}}}
	require.Nil(t, service.BuildContinuationRequest(ctx, current, history), "another agent's hosted instruction state must not be reused")
}

// A native model type is used only as an identity witness; these unit tests
// never call a model or return simulated acceptance responses.
type contractModel struct{}

func (contractModel) Implements(string) bool { return false }
func (contractModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	panic("unit identity witness must not generate")
}

type otherContractModel struct{ contractModel }

func TestContinuationContractBoundaries(t *testing.T) {
	for _, name := range []string{"same", "agent", "instructions", "ordinary-system", "tools", "tool-schema", "owner", "model", "provider", "choice", "refreshed-docs", "caller-fingerprint", "missing-anchor-contract", "wire-model", "unverified-owner", "rolling-metadata"} {
		t.Run(name, func(t *testing.T) {
			ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
			ctx = requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "contract-conversation"})
			anchorAt := time.Now()
			history := &binding.History{LastResponse: &binding.Trace{ID: "response-anchor", At: anchorAt}, Traces: map[string]*binding.Trace{binding.ContentMessageKey("new-user"): {At: anchorAt.Add(time.Second)}}}
			makeRequest := func() *llm.GenerateRequest {
				return &llm.GenerateRequest{Instructions: "ordinary instruction", Messages: []llm.Message{{Role: llm.RoleSystem, Content: "ordinary system"}, {ID: "new-user", Role: llm.RoleUser, Content: "new task"}}, Options: &llm.Options{Model: "wire-model", Tools: []llm.Tool{llm.NewFunctionTool(llm.ToolDefinition{Name: "tool-a", Parameters: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}})}}}
			}
			input := &GenerateInput{AgentID: "agent-a", ModelSelection: llm.ModelSelection{Model: "registry-model"}, UserID: "untrusted-caller-id"}
			previous := makeRequest()
			_ = withContinuationContract(ctx, previous, input, contractModel{})
			raw, err := json.Marshal(previous)
			require.NoError(t, err)
			store := &contractAnchorClient{request: raw}
			service := &Service{convClient: store}
			current := makeRequest()
			var model llm.Model = contractModel{}
			switch name {
			case "agent":
				input.AgentID = "agent-b"
			case "instructions":
				current.Instructions = "changed"
			case "ordinary-system":
				current.Messages[0].Content = "changed"
			case "tools":
				current.Options.Tools = nil
			case "tool-schema":
				current.Options.Tools[0].Definition.Parameters = map[string]any{"type": "array"}
			case "owner":
				ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "other"})
			case "model":
				input.Model = "other-model"
			case "provider":
				model = otherContractModel{}
			case "choice":
				current.Options.ToolChoice = llm.NewNoneToolChoice()
			case "refreshed-docs":
				current.Messages = append([]llm.Message{{Role: llm.RoleSystem, Content: "updated trusted template", RefreshOnContinuation: true}}, current.Messages...)
			case "caller-fingerprint":
				current.Options.Metadata = map[string]any{continuationContractMetadata: "caller-forged"}
				input.UserID = "other-untrusted-identity"
			case "wire-model":
				current.Options.Model = "different-wire-model"
			case "unverified-owner":
				ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{})
			case "rolling-metadata":
				current.Options.Metadata = map[string]any{"lease": "changed", "timestamp": "later", "cache": "different"}
			case "missing-anchor-contract":
				delete(previous.Options.Metadata, continuationContractMetadata)
				store.request, _ = json.Marshal(previous)
			}
			ctx = withContinuationContract(ctx, current, input, model)
			continuation := service.BuildContinuationRequest(ctx, current, history)
			expected := name == "same" || name == "refreshed-docs" || name == "caller-fingerprint" || name == "rolling-metadata"
			if expected {
				require.NotNil(t, continuation)
				require.Equal(t, "response-anchor", continuation.PreviousResponseID)
			} else {
				require.Nil(t, continuation)
			}
		})
	}
}

func scopedTestContinuation(t *testing.T, s *Service, ctx context.Context, req *llm.GenerateRequest, h *binding.History) *llm.GenerateRequest {
	t.Helper()
	if req == nil || h == nil || h.LastResponse == nil {
		return s.BuildContinuationRequest(ctx, req, h)
	}
	if iauth.User(ctx) == nil {
		ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "owner"})
	}
	input := &GenerateInput{AgentID: "fixture-agent", ModelSelection: llm.ModelSelection{Model: "fixture-model"}}
	ctx = withContinuationContract(ctx, req, input, contractModel{})
	b, err := json.Marshal(req)
	require.NoError(t, err)
	if s.convClient == nil {
		s.convClient = &contractAnchorClient{request: b, anchorID: h.LastResponse.ID}
	} else if existing, ok := s.convClient.(*contractAnchorClient); ok && existing.conversation == nil {
		existing.request = b
		existing.anchorID = h.LastResponse.ID
	}
	return s.BuildContinuationRequest(ctx, req, h)
}

func TestContinuationVerifiedAuthorityAndConnection(t *testing.T) {
	for _, name := range []string{"same", "account", "tenant", "issuer", "subject", "revision", "expired", "revoked", "unbound", "unknown-connection", "endpoint", "release-changed", "prior-turn-unbound"} {
		t.Run(name, func(t *testing.T) {
			actor := resource.VerifiedActor{Subject: "owner", Issuer: "issuer", TenantID: "tenant-a", AccountID: "account-a", IdentityRevision: "revision", ValidUntil: time.Now().Add(time.Hour)}
			ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
			ctx = requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "contract-conversation"})
			input := &GenerateInput{AgentID: "agent", ModelSelection: llm.ModelSelection{Model: "model"}}
			model := openai.NewClient("fixture-only", "model", openai.WithBaseURL("http://127.0.0.1:9999"))
			prior := &llm.GenerateRequest{Options: &llm.Options{}}
			priorCtx := context.WithValue(ctx, continuationAuthorityKey{}, actorContractScope(actor))
			priorCtx = context.WithValue(priorCtx, continuationConnectionKey{}, continuationConnection(model))
			withContinuationContract(priorCtx, prior, input, model)
			if name == "unbound" {
				delete(prior.Options.Metadata, continuationContractMetadata)
			}
			encoded, _ := json.Marshal(prior)
			store := &contractAnchorClient{request: encoded}
			if name == "prior-turn-unbound" {
				store.conversation = &apiconv.Conversation{Id: "contract-conversation", CreatedByUserId: ptrContract("owner"), Transcript: []*conversationmodel.TranscriptView{{Id: "old-turn", Message: []*conversationmodel.MessageView{{Id: "old-user", Role: "user"}}}}}
			}
			service := &Service{convClient: store}
			revoked := false
			service.SetContinuationAuthority(func(context.Context) (resource.VerifiedActor, error) { return actor, nil }, func(context.Context, resource.VerifiedActor) error {
				if revoked {
					return errors.New("revoked")
				}
				return nil
			})
			switch name {
			case "account":
				actor.AccountID = "account-b"
			case "tenant":
				actor.TenantID = "tenant-b"
			case "issuer":
				actor.Issuer = "other"
			case "subject":
				actor.Subject = "other"
			case "revision":
				actor.IdentityRevision = "other"
			case "expired":
				actor.ValidUntil = time.Now().Add(-time.Second)
			case "revoked":
				revoked = true
			case "unknown-connection":
				model.APIKeyProvider = func(context.Context) (string, error) { panic("credentials must not be consulted") }
			case "endpoint":
				model.BaseURL = "http://127.0.0.1:9998"
			}
			req := &llm.GenerateRequest{Options: &llm.Options{}}
			result, err := service.prepareContinuationContract(ctx, req, input, model)
			if name != "same" && name != "unknown-connection" && name != "endpoint" && name != "release-changed" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if name == "unknown-connection" {
				require.Nil(t, result.Value(continuationContractContextKey{}))
				return
			}
			if name == "endpoint" {
				require.False(t, service.continuationContractMatches(result, "contract-conversation", "response-anchor"))
				return
			}
			if name == "release-changed" {
				actor.AccountID = "account-b"
				require.Error(t, service.verifyContinuationRelease(result))
				return
			}
			require.True(t, service.continuationContractMatches(result, "contract-conversation", "response-anchor"))
		})
	}
}
