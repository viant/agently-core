package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/openai"
	iauth "github.com/viant/agently-core/internal/auth"
	resource "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"net/url"
	"sort"
	"strings"
	"time"
)

const continuationContractMetadata = "agentlyContinuationContractV1"

type continuationContractContextKey struct{}
type continuationAuthorityKey struct{}
type continuationConnectionKey struct{}
type continuationContract struct {
	Version      int    `json:"version"`
	Owner        string `json:"owner"`
	Agent        string `json:"agent"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	WireModel    string `json:"wireModel"`
	Instructions string `json:"instructions"`
	Systems      string `json:"systems"`
	Tools        string `json:"tools"`
	Choice       string `json:"choice"`
}

func contractDigest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// The proof comes from verified request context and the resolved generation
// inputs, never from a caller-supplied fingerprint or raw GenerateInput.UserID.
func withContinuationContract(ctx context.Context, request *llm.GenerateRequest, input *GenerateInput, model llm.Model) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, continuationContractContextKey{}, (*continuationContract)(nil))
	if request == nil {
		return ctx
	}
	if request.Options == nil {
		request.Options = &llm.Options{}
	}
	options := *request.Options
	options.Metadata = map[string]any{}
	for key, value := range request.Options.Metadata {
		if key != continuationContractMetadata {
			options.Metadata[key] = value
		}
	}
	request.Options = &options
	owner := iauth.EffectiveUserID(ctx)
	if owner == "" || input == nil || strings.TrimSpace(input.AgentID) == "" || strings.TrimSpace(input.Model) == "" || model == nil {
		return ctx
	}
	scope, _ := ctx.Value(continuationAuthorityKey{}).(string)
	connection, _ := ctx.Value(continuationConnectionKey{}).(string)
	if scope == "" {
		scope = contractDigest([]string{iauth.Provider(ctx), iauth.CanonicalUserID(ctx), owner})
	}
	proof := &continuationContract{Version: 2, Owner: scope, Agent: strings.TrimSpace(input.AgentID), Provider: fmt.Sprintf("%T:%s", model, connection), Model: strings.TrimSpace(input.Model), WireModel: request.Options.Model, Instructions: contractDigest(request.Instructions), Choice: contractDigest(request.Options.ToolChoice)}
	ordinary := []struct {
		Content string
		Name    string
		Items   []llm.ContentItem
	}{}
	for _, message := range request.Messages {
		if message.Role == llm.RoleSystem && !message.RefreshOnContinuation {
			ordinary = append(ordinary, struct {
				Content string
				Name    string
				Items   []llm.ContentItem
			}{message.Content, message.Name, message.Items})
		}
	}
	proof.Systems = contractDigest(ordinary)
	definitions := make([]string, 0, len(request.Options.Tools))
	for _, tool := range request.Options.Tools {
		b, err := json.Marshal(tool)
		if err != nil {
			return ctx
		}
		definitions = append(definitions, string(b))
	}
	sort.Strings(definitions)
	proof.Tools = contractDigest(definitions)
	if proof.Owner == "" || proof.Instructions == "" || proof.Systems == "" || proof.Tools == "" || proof.Choice == "" {
		return ctx
	}
	request.Options.Metadata[continuationContractMetadata] = *proof
	return context.WithValue(ctx, continuationContractContextKey{}, proof)
}
func (s *Service) continuationContractMatches(ctx context.Context, conversationID, anchorID string) bool {
	current, _ := ctx.Value(continuationContractContextKey{}).(*continuationContract)
	if current == nil || s == nil || s.convClient == nil {
		return false
	}
	options := []apiconv.Option{apiconv.WithIncludeModelCall(true), apiconv.WithIncludeToolCall(false)}
	// Since is a turn ID, not a timestamp. Do not reinterpret that public
	// selector; the scoped reader below must actually contain the anchor.
	conversation, err := s.convClient.GetConversation(ctx, conversationID, options...)
	if err != nil || conversation == nil {
		return false
	}
	owner := iauth.EffectiveUserID(ctx)
	if owner == "" || conversation.CreatedByUserId == nil || strings.TrimSpace(*conversation.CreatedByUserId) != owner {
		return false
	}
	for _, turn := range conversation.GetTranscript() {
		if turn == nil {
			continue
		}
		for _, message := range turn.GetMessages() {
			if message == nil || message.ModelCall == nil || message.ModelCall.TraceId == nil || *message.ModelCall.TraceId != anchorID || message.ModelCall.RequestPayloadId == nil {
				continue
			}
			payload, err := s.convClient.GetPayload(ctx, *message.ModelCall.RequestPayloadId)
			if err != nil || payload == nil || payload.InlineBody == nil {
				return false
			}
			decoded := apiconv.DecodeInlineBody(string(*payload.InlineBody), payload.Compression)
			var previous llm.GenerateRequest
			if json.Unmarshal([]byte(decoded), &previous) != nil || previous.Options == nil {
				return false
			}
			raw, ok := previous.Options.Metadata[continuationContractMetadata]
			if !ok {
				return false
			}
			b, err := json.Marshal(raw)
			if err != nil {
				return false
			}
			var proof continuationContract
			if json.Unmarshal(b, &proof) != nil || proof.Version != 2 {
				return false
			}
			return proof == *current
		}
	}
	return false
}

// SetContinuationAuthority installs host-verified authority before requests start.
// Existing explicitly configured authority is not replaced by builder defaults.
func (s *Service) SetContinuationAuthority(resolve func(context.Context) (resource.VerifiedActor, error), verify func(context.Context, resource.VerifiedActor) error) {
	if s == nil || s.continuationActorResolver != nil || resolve == nil {
		return
	}
	s.continuationActorResolver, s.continuationActorVerifier = resolve, verify
}

func actorContractScope(actor resource.VerifiedActor) string {
	return contractDigest([]string{actor.Issuer, actor.Subject, actor.TenantID, actor.AccountID, actor.IdentityRevision})
}

// No credentials are read or hashed. Dynamic credential/account registries have
// no proven connection identity and therefore cannot retain hosted anchors.
func continuationConnection(model llm.Model) string {
	client, ok := model.(*openai.Client)
	if !ok || client.APIKeyProvider != nil || client.ChatGPTAccountIDProvider != nil {
		return ""
	}
	endpoint := strings.TrimSpace(client.BaseURL)
	parsed, err := url.Parse(endpoint)
	// A credential-bearing URL is not a logical connection identity.
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	endpoint = strings.TrimRight(parsed.String(), "/")
	if endpoint == "" || strings.TrimSpace(client.Model) == "" {
		return ""
	}
	return contractDigest([]string{"openai", endpoint, client.Model, client.ChatGPTAccountID})
}

func (s *Service) prepareContinuationContract(ctx context.Context, req *llm.GenerateRequest, in *GenerateInput, model llm.Model) (context.Context, error) {
	// Legacy unconfigured deployments retain their full-history behavior, but
	// cannot prove the authority of hosted provider state.
	if s.continuationActorResolver == nil {
		ctx = withContinuationContract(ctx, req, in, model)
		delete(req.Options.Metadata, continuationContractMetadata)
		return context.WithValue(ctx, continuationContractContextKey{}, (*continuationContract)(nil)), nil
	}
	actor, err := s.continuationActorResolver(ctx)
	if iauth.EffectiveUserID(ctx) == "" {
		return ctx, errors.New("continuation verified user unavailable")
	}
	if err != nil || !actor.Valid(time.Now()) || s.continuationActorVerifier == nil {
		return ctx, errors.New("continuation authority unavailable")
	}
	if err = s.continuationActorVerifier(ctx, actor); err != nil {
		return ctx, errors.New("continuation authority rejected")
	}
	scope := actorContractScope(actor)
	// This gate applies before either anchor dispatch or full-history replay.
	id := requestctx.ConversationIDFromContext(ctx)
	if id != "" && s.convClient != nil {
		convo, e := s.convClient.GetConversation(ctx, id, apiconv.WithIncludeModelCall(true), apiconv.WithIncludeToolCall(false))
		if e != nil || convo == nil {
			return ctx, errors.New("continuation conversation scope unavailable")
		}
		owner := iauth.EffectiveUserID(ctx)
		if owner == "" || convo.CreatedByUserId == nil || strings.TrimSpace(*convo.CreatedByUserId) != owner {
			return ctx, errors.New("continuation conversation owner rejected")
		}
		currentTurn, _ := requestctx.TurnMetaFromContext(ctx)
		for _, turn := range convo.GetTranscript() {
			if turn == nil {
				continue
			}
			bound := false
			for _, message := range turn.GetMessages() {
				if message == nil || message.ModelCall == nil {
					continue
				}
				if message.ModelCall.RequestPayloadId == nil {
					return ctx, errors.New("continuation history authority unbound")
				}
				payload, e := s.convClient.GetPayload(ctx, *message.ModelCall.RequestPayloadId)
				if e != nil || payload == nil || payload.InlineBody == nil {
					return ctx, errors.New("continuation history authority unbound")
				}
				var previous llm.GenerateRequest
				if json.Unmarshal([]byte(apiconv.DecodeInlineBody(string(*payload.InlineBody), payload.Compression)), &previous) != nil || previous.Options == nil {
					return ctx, errors.New("continuation history authority unbound")
				}
				raw, ok := previous.Options.Metadata[continuationContractMetadata]
				if !ok {
					return ctx, errors.New("continuation history authority unbound")
				}
				encoded, e := json.Marshal(raw)
				if e != nil {
					return ctx, errors.New("continuation history authority unbound")
				}
				var stored continuationContract
				if json.Unmarshal(encoded, &stored) != nil || stored.Version != 2 || stored.Owner == "" {
					return ctx, errors.New("continuation history authority unbound")
				}
				if stored.Owner != scope {
					return ctx, errors.New("continuation history authority changed")
				}
				bound = true
			}
			if !bound && len(turn.GetMessages()) > 0 && (currentTurn.TurnID == "" || turn.Id != currentTurn.TurnID) {
				return ctx, errors.New("continuation prior turn authority unbound")
			}
		}
	}
	ctx = context.WithValue(ctx, continuationAuthorityKey{}, scope)
	connection := continuationConnection(model)
	ctx = context.WithValue(ctx, continuationConnectionKey{}, connection)
	ctx = withContinuationContract(ctx, req, in, model)
	// Store authority even when the connection is unproven: subsequent history
	// still requires authorization, while hosted anchor reuse remains disabled.
	if connection == "" {
		ctx = context.WithValue(ctx, continuationContractContextKey{}, (*continuationContract)(nil))
	}
	return ctx, nil
}

func (s *Service) verifyContinuationRelease(ctx context.Context) error {
	if s.continuationActorResolver == nil {
		return nil
	}
	expected, _ := ctx.Value(continuationAuthorityKey{}).(string)
	actor, err := s.continuationActorResolver(ctx)
	if err != nil || !actor.Valid(time.Now()) || expected == "" || actorContractScope(actor) != expected || s.continuationActorVerifier == nil {
		return errors.New("continuation authority changed before dispatch")
	}
	if s.continuationActorVerifier(ctx, actor) != nil {
		return errors.New("continuation authority rejected before dispatch")
	}
	return nil
}
