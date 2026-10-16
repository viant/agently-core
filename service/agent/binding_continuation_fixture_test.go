package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/openai"
	iauth "github.com/viant/agently-core/internal/auth"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/agently-core/protocol/binding"
	resource "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/core"
)

// Capture the actual host-prepared generation contract at the persistence
// boundary. The in-memory HTTP transport below prevents any provider I/O.
var errContinuationFixtureCaptured = errors.New("continuation fixture request captured")

type continuationFixtureStore struct {
	apiconv.Client
	owner   string
	anchor  string
	ctx     context.Context
	request []byte
}

func (s *continuationFixtureStore) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	out := &apiconv.Conversation{CreatedByUserId: &s.owner}
	if s.anchor != "" {
		payload := "verified-anchor-request"
		out.Transcript = []*conversationmodel.TranscriptView{{Message: []*conversationmodel.MessageView{{ModelCall: &conversationmodel.ModelCallView{TraceId: &s.anchor, RequestPayloadId: &payload}}}}}
	}
	return out, nil
}
func (s *continuationFixtureStore) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	b := append([]byte(nil), s.request...)
	return &apiconv.Payload{InlineBody: &b, Compression: "none"}, nil
}
func (s *continuationFixtureStore) PatchMessage(context.Context, *apiconv.MutableMessage) error {
	return nil
}
func (s *continuationFixtureStore) PatchModelCall(context.Context, *apiconv.MutableModelCall) error {
	return nil
}
func (s *continuationFixtureStore) PatchPayload(ctx context.Context, p *apiconv.MutablePayload) error {
	var request llm.GenerateRequest
	if p.InlineBody != nil && json.Unmarshal(*p.InlineBody, &request) == nil && request.Options != nil && request.Options.Metadata["agentlyContinuationContractV1"] != nil {
		s.ctx = ctx
		s.request = append([]byte(nil), (*p.InlineBody)...)
		return nil
	}
	return nil
}

type continuationFixtureTransport struct{}

func (continuationFixtureTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errContinuationFixtureCaptured
}

type continuationFixtureFinder struct{ model llm.Model }

func (f continuationFixtureFinder) Find(context.Context, string) (llm.Model, error) {
	return f.model, nil
}

func verifiedContinuationFixture(t *testing.T, ctx context.Context, request *llm.GenerateRequest, anchor string) (*core.Service, context.Context) {
	t.Helper()
	store := &continuationFixtureStore{owner: "fixture-owner"}
	// A static logical connection identity is required by the hosted anchor
	// contract. The transport returns a sentinel without opening a connection.
	model := openai.NewClient("fixture-key", "fixture-model")
	model.BaseURL = "http://fixture.invalid"
	model.HTTPClient = &http.Client{Transport: continuationFixtureTransport{}}
	service := core.New(continuationFixtureFinder{model: model}, nil, store)
	actor := resource.VerifiedActor{Issuer: "fixture-issuer", Subject: store.owner, TenantID: "1", AccountID: "1", IdentityRevision: "fixture-revision", ValidUntil: time.Now().Add(time.Minute)}
	service.SetContinuationAuthority(func(context.Context) (resource.VerifiedActor, error) { return actor, nil }, func(context.Context, resource.VerifiedActor) error { return nil })
	ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: store.owner})
	input := &core.GenerateInput{AgentID: "fixture-agent", UserID: store.owner, ModelSelection: llm.ModelSelection{Model: "fixture-model"}, Message: request.Messages, Instructions: request.Instructions, Binding: &binding.Binding{}}
	err := service.Generate(ctx, input, &core.GenerateOutput{})
	require.ErrorIs(t, err, errContinuationFixtureCaptured)
	require.NotNil(t, store.ctx)
	require.NotEmpty(t, store.request)
	store.anchor = anchor
	return service, store.ctx
}
