package forecastbinding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	evidence "github.com/viant/agently-core/app/store/reportingevidence"
	authctx "github.com/viant/agently-core/internal/auth"
	runmodel "github.com/viant/agently-core/model/run"
)

type ConversationReader interface {
	GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error)
}
type NativeRunAccess interface {
	GetRun(context.Context, string, *runmodel.RunRowsInput, ...data.Option) (*runmodel.RunRowsView, error)
}

// NativeSourceStore uses authorized native APIs and immutable run-owned evidence
// documents. It never changes the execution checkpoint.
type NativeSourceStore struct {
	conversations ConversationReader
	runs          NativeRunAccess
	documents     evidence.Documents
}

func NewNativeSourceStore(c ConversationReader, r NativeRunAccess, documents ...evidence.Documents) *NativeSourceStore {
	result := &NativeSourceStore{conversations: c, runs: r}
	if len(documents) == 1 {
		result.documents = documents[0]
	}
	return result
}
func checkPrincipal(ctx context.Context, scope Scope) error {
	if scope.OwnerID == "" || scope.ConversationID == "" || scope.TurnID == "" || authctx.EffectiveUserID(ctx) != scope.OwnerID {
		return reject("authenticated scope mismatch")
	}
	return nil
}
func (s *NativeSourceStore) scopedRun(ctx context.Context, scope Scope) (*runmodel.RunRowsView, error) {
	if e := checkPrincipal(ctx, scope); e != nil {
		return nil, e
	}
	if s.runs == nil {
		return nil, reject("native run store unavailable")
	}
	r, e := s.runs.GetRun(ctx, scope.TurnID, nil)
	if e != nil {
		return nil, e
	}
	if r == nil || r.EffectiveUserId == nil || *r.EffectiveUserId != scope.OwnerID || r.ConversationId == nil || *r.ConversationId != scope.ConversationID || r.TurnId == nil || *r.TurnId != scope.TurnID {
		return nil, reject("native run scope mismatch")
	}
	return r, nil
}
func (s *NativeSourceStore) LoadCompletedCall(ctx context.Context, scope Scope, op string) (*Call, error) {
	return s.loadNativeCall(ctx, scope, op, "", true)
}

func (s *NativeSourceStore) loadNativeCall(ctx context.Context, scope Scope, op, messageID string, completed bool) (*Call, error) {
	if e := checkPrincipal(ctx, scope); e != nil {
		return nil, e
	}
	if s.conversations == nil || (op == "" && messageID == "") {
		return nil, reject("source store unavailable")
	}
	c, e := s.conversations.GetConversation(ctx, scope.ConversationID, apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
	if e != nil {
		return nil, e
	}
	owner := authctx.CanonicalUserID(ctx)
	if owner == "" {
		owner = scope.OwnerID
	}
	if c == nil || c.Id != scope.ConversationID || c.CreatedByUserId == nil || (*c.CreatedByUserId != owner && *c.CreatedByUserId != scope.OwnerID) {
		return nil, reject("conversation owner mismatch")
	}
	var found *Call
	for _, turn := range c.GetTranscript() {
		if turn == nil || turn.Id != scope.TurnID || turn.ConversationId != scope.ConversationID {
			continue
		}
		for _, m := range turn.Message {
			if m == nil || m.ConversationId != scope.ConversationID || m.TurnId == nil || *m.TurnId != scope.TurnID {
				continue
			}
			tc := m.MessageToolCall
			if tc == nil || (op != "" && tc.OpId != op) || (messageID != "" && m.Id != messageID) {
				continue
			}
			if found != nil {
				return nil, reject("ambiguous source op")
			}
			if tc.TurnId == nil || *tc.TurnId != scope.TurnID || tc.MessageRequestPayload == nil || tc.MessageRequestPayload.InlineBody == nil || (completed && (tc.Status != "completed" || tc.MessageResponsePayload == nil || tc.MessageResponsePayload.InlineBody == nil)) {
				return nil, reject("incomplete source evidence")
			}
			request := apiconv.DecodeInlineBody(*tc.MessageRequestPayload.InlineBody, tc.MessageRequestPayload.Compression)
			response := ""
			if tc.MessageResponsePayload != nil && tc.MessageResponsePayload.InlineBody != nil {
				response = apiconv.DecodeInlineBody(*tc.MessageResponsePayload.InlineBody, tc.MessageResponsePayload.Compression)
			}
			if !json.Valid([]byte(request)) || (response != "" && !json.Valid([]byte(response))) {
				return nil, reject("invalid source payload")
			}
			found = &Call{Scope: scope, MessageID: m.Id, OpID: tc.OpId, Tool: tc.ToolName, Status: tc.Status, Request: json.RawMessage(request), Response: json.RawMessage(response)}
		}
	}
	if found == nil {
		return nil, reject("source op not found in turn")
	}
	return found, nil
}

func (s *NativeSourceStore) documentScope(ctx context.Context, scope Scope) (evidence.Scope, error) {
	r, err := s.scopedRun(ctx, scope)
	if err != nil {
		return evidence.Scope{}, err
	}
	if s.documents == nil {
		return evidence.Scope{}, reject("immutable evidence store unavailable")
	}
	return evidence.Scope{OwnerID: scope.OwnerID, ConversationID: scope.ConversationID, TurnID: scope.TurnID, RunID: r.Id}, nil
}
func (s *NativeSourceStore) LoadAdmission(ctx context.Context, scope Scope) (*Admission, error) {
	doc, err := s.documentScope(ctx, scope)
	if err != nil {
		return nil, err
	}
	body, err := s.documents.Load(ctx, doc, evidence.Admission, "")
	if errors.Is(err, evidence.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a Admission
	if err = json.Unmarshal(body, &a); err != nil {
		return nil, err
	}
	if a.Scope != scope {
		return nil, reject("admission scope mismatch")
	}
	if err = a.Validate(); err != nil {
		return nil, err
	}
	return &a, nil
}
func (s *NativeSourceStore) SaveAdmission(ctx context.Context, a Admission, lease string) error {
	if err := a.Validate(); err != nil {
		return err
	}
	doc, err := s.documentScope(ctx, a.Scope)
	if err != nil {
		return err
	}
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return s.documents.Save(ctx, doc, evidence.Admission, "", lease, body)
}
func (s *NativeSourceStore) LoadPlan(ctx context.Context, scope Scope, id string) (*Plan, error) {
	doc, err := s.documentScope(ctx, scope)
	if err != nil {
		return nil, err
	}
	body, err := s.documents.Load(ctx, doc, evidence.Plan, id)
	if err != nil {
		return nil, err
	}
	var p Plan
	if err = json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	if p.ID != id || p.Admission.Scope != scope {
		return nil, reject("plan scope mismatch")
	}
	if err = p.ValidateIdentity(); err != nil {
		return nil, err
	}
	return &p, nil
}
func (s *NativeSourceStore) SavePlan(ctx context.Context, p *Plan, lease string) error {
	if p == nil {
		return fmt.Errorf("forecast evidence: nil plan")
	}
	if err := p.ValidateIdentity(); err != nil {
		return err
	}
	admission, err := s.LoadAdmission(ctx, p.Admission.Scope)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(admission)
	b, _ := json.Marshal(p.Admission)
	if string(a) != string(b) {
		return reject("plan admission mismatch")
	}
	doc, err := s.documentScope(ctx, p.Admission.Scope)
	if err != nil {
		return err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.documents.Save(ctx, doc, evidence.Plan, p.ID, lease, body)
}

// PublishCompletedProjection updates only the printable tool-message projection.
// The original request/response payload links remain immutable evidence. History
// uses GetContentPreferContent, so the receipt survives provider continuation and
// process restart without rewriting the underlying source response.
func (s *NativeSourceStore) PublishCompletedProjection(ctx context.Context, scope Scope, op string, body json.RawMessage) error {
	call, e := s.LoadCompletedCall(ctx, scope, op)
	if e != nil {
		return e
	}
	if call.MessageID == "" {
		return reject("source message identity missing")
	}
	value, e := object(body)
	if e != nil {
		return e
	}
	var expected json.RawMessage
	runtime := &Runtime{store: s}
	switch {
	case value[ReceiptKey] != nil:
		receiptRaw, _ := json.Marshal(value[ReceiptKey])
		var receipt Receipt
		if e = json.Unmarshal(receiptRaw, &receipt); e != nil || receipt.OpID != op {
			return reject("projection receipt identity mismatch")
		}
		expected, e = runtime.DecorateCompleted(ctx, scope, receipt.PlanID, op)
	case value[PlanReceiptKey] != nil:
		receiptRaw, _ := json.Marshal(value[PlanReceiptKey])
		var receipt PlanReceipt
		if e = json.Unmarshal(receiptRaw, &receipt); e != nil {
			return e
		}
		expected, e = runtime.planProjection(ctx, call, receipt.PlanID)
	case value[SourceReceiptKey] != nil || value[ProfileReceiptKey] != nil:
		expected, e = sourceProjection(call)
	default:
		return reject("projection receipt missing")
	}
	if e != nil {
		return e
	}
	expectedCanonical, e := canonical(expected)
	if e != nil {
		return e
	}
	normalized, e := canonical(body)
	if e != nil || string(expectedCanonical) != string(normalized) {
		return reject("projection receipt mismatch")
	}
	client, ok := s.conversations.(interface {
		PatchMessage(context.Context, *apiconv.MutableMessage) error
		GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error)
	})
	if !ok {
		return reject("source projection writer unavailable")
	}
	patch := apiconv.NewMessage()
	patch.SetId(call.MessageID)
	patch.SetConversationID(scope.ConversationID)
	patch.SetTurnID(scope.TurnID)
	patch.SetContent(string(body))
	if e = client.PatchMessage(ctx, patch); e != nil {
		return e
	}
	confirmed, e := client.GetMessage(ctx, call.MessageID, apiconv.WithIncludeToolCall(true))
	if e != nil {
		return e
	}
	if confirmed == nil || confirmed.ConversationId != scope.ConversationID || confirmed.TurnId == nil || *confirmed.TurnId != scope.TurnID || confirmed.Content == nil || *confirmed.Content != string(body) {
		return reject("source projection write unconfirmed")
	}
	return nil
}

// CompletedOperations advertises identifiers only; the selected operation is
// subsequently reloaded with its immutable payloads before it can authorize a
// request. Neither transcript order nor a foreign turn can select a source.
func (s *NativeSourceStore) CompletedOperations(ctx context.Context, scope Scope, tool string) ([]string, error) {
	if e := checkPrincipal(ctx, scope); e != nil {
		return nil, e
	}
	if s.conversations == nil {
		return nil, reject("source store unavailable")
	}
	conversation, e := s.conversations.GetConversation(ctx, scope.ConversationID, apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
	if e != nil {
		return nil, e
	}
	owner := authctx.CanonicalUserID(ctx)
	if owner == "" {
		owner = scope.OwnerID
	}
	if conversation == nil || conversation.Id != scope.ConversationID || conversation.CreatedByUserId == nil || (*conversation.CreatedByUserId != owner && *conversation.CreatedByUserId != scope.OwnerID) {
		return nil, reject("conversation owner mismatch")
	}
	var result []string
	seen := map[string]bool{}
	for _, turn := range conversation.GetTranscript() {
		if turn == nil || turn.Id != scope.TurnID || turn.ConversationId != scope.ConversationID {
			continue
		}
		for _, message := range turn.Message {
			if message == nil || message.ConversationId != scope.ConversationID || message.TurnId == nil || *message.TurnId != scope.TurnID {
				continue
			}
			call := message.MessageToolCall
			if call == nil || call.ToolName != tool || call.Status != "completed" || call.TurnId == nil || *call.TurnId != scope.TurnID {
				continue
			}
			if call.OpId == "" || seen[call.OpId] {
				return nil, reject("ambiguous source op")
			}
			seen[call.OpId] = true
			result = append(result, call.OpId)
		}
	}
	return result, nil
}

// OwnedRunCreatedAt proves a legacy resume against server-owned run and
// conversation metadata. Wire input and current clock cannot establish it.
func (s *NativeSourceStore) OwnedRunCreatedAt(ctx context.Context, scope Scope) (time.Time, error) {
	run, err := s.scopedRun(ctx, scope)
	if err != nil {
		return time.Time{}, err
	}
	if s.conversations == nil {
		return time.Time{}, reject("conversation owner unavailable")
	}
	conversation, err := s.conversations.GetConversation(ctx, scope.ConversationID)
	if err != nil {
		return time.Time{}, err
	}
	owner := authctx.CanonicalUserID(ctx)
	if owner == "" {
		owner = scope.OwnerID
	}
	if conversation == nil || conversation.Id != scope.ConversationID || conversation.CreatedByUserId == nil || (*conversation.CreatedByUserId != owner && *conversation.CreatedByUserId != scope.OwnerID) {
		return time.Time{}, reject("legacy conversation owner mismatch")
	}
	if run.CreatedAt.IsZero() {
		return time.Time{}, reject("server run creation time missing")
	}
	return run.CreatedAt, nil
}
