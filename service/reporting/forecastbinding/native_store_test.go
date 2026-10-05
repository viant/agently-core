package forecastbinding

import (
	"context"
	"encoding/json"
	"testing"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	evidence "github.com/viant/agently-core/app/store/reportingevidence"
	authctx "github.com/viant/agently-core/internal/auth"
	runmodel "github.com/viant/agently-core/model/run"
)

type nativeFixture struct {
	conversation *apiconv.Conversation
	run          *runmodel.RunRowsView
	loseLease    bool
	writes       int
	documents    map[string]json.RawMessage
}

func (f *nativeFixture) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	b, _ := json.Marshal(f.conversation)
	var c apiconv.Conversation
	_ = json.Unmarshal(b, &c)
	return &c, nil
}
func (f *nativeFixture) GetRun(context.Context, string, *runmodel.RunRowsInput, ...data.Option) (*runmodel.RunRowsView, error) {
	b, _ := json.Marshal(f.run)
	var r runmodel.RunRowsView
	_ = json.Unmarshal(b, &r)
	return &r, nil
}
func (f *nativeFixture) Load(_ context.Context, scope evidence.Scope, subtype, id string) (json.RawMessage, error) {
	body := f.documents[evidence.ID(scope, subtype, id)]
	if len(body) == 0 {
		return nil, reject("document missing")
	}
	return append(json.RawMessage(nil), body...), nil
}
func (f *nativeFixture) Save(_ context.Context, scope evidence.Scope, subtype, id, lease string, body json.RawMessage) error {
	if f.loseLease || f.run.LeaseOwner == nil || *f.run.LeaseOwner != lease {
		return reject("lease mismatch")
	}
	if f.documents == nil {
		f.documents = map[string]json.RawMessage{}
	}
	key := evidence.ID(scope, subtype, id)
	if old := f.documents[key]; len(old) > 0 && string(old) != string(body) {
		return reject("immutable conflict")
	}
	f.documents[key] = append(json.RawMessage(nil), body...)
	f.writes++
	return nil
}
func nativeTestStore(t *testing.T) (*NativeSourceStore, *nativeFixture, context.Context, Admission) {
	t.Helper()
	f := loadFixture(t)
	a := actualAdmission(t)
	call := *firstCall(&f)
	wire := map[string]any{"id": a.ConversationID, "createdByUserId": a.OwnerID, "transcript": []any{map[string]any{"id": a.TurnID, "conversationId": a.ConversationID, "message": []any{map[string]any{"id": "tool-message", "conversationId": a.ConversationID, "turnId": a.TurnID, "role": "tool", "messageToolCall": map[string]any{"opId": call.OpID, "toolName": call.Tool, "status": "completed", "turnId": a.TurnID, "messageRequestPayload": map[string]any{"inlineBody": string(call.Request)}, "messageResponsePayload": map[string]any{"inlineBody": string(call.Response)}}}}}}}
	raw, _ := json.Marshal(wire)
	var conversation apiconv.Conversation
	if e := json.Unmarshal(raw, &conversation); e != nil {
		t.Fatal(e)
	}
	lease := "worker"
	checkpoint := `{"unrelated":{"retain":true}}`
	fixture := &nativeFixture{conversation: &conversation, run: &runmodel.RunRowsView{Id: a.TurnID, ConversationId: &a.ConversationID, TurnId: &a.TurnID, EffectiveUserId: &a.OwnerID, Status: "running", Attempt: 1, LeaseOwner: &lease, CheckpointData: &checkpoint}}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: a.OwnerID})
	return NewNativeSourceStore(fixture, fixture, fixture), fixture, ctx, a
}
func TestNativeSourceStoreAuthenticatesScopeAndCompleteness(t *testing.T) {
	s, f, ctx, a := nativeTestStore(t)
	fixture := loadFixture(t)
	op := firstCall(&fixture).OpID
	if _, e := s.LoadCompletedCall(ctx, a.Scope, op); e != nil {
		t.Fatal(e)
	}
	if _, e := s.LoadCompletedCall(context.Background(), a.Scope, op); e == nil {
		t.Fatal("missing authenticated owner accepted")
	}
	foreign := a.Scope
	foreign.TurnID = "foreign"
	if _, e := s.LoadCompletedCall(ctx, foreign, op); e == nil {
		t.Fatal("foreign turn accepted")
	}
	owner := "foreign"
	f.conversation.CreatedByUserId = &owner
	if _, e := s.LoadCompletedCall(ctx, a.Scope, op); e == nil {
		t.Fatal("foreign conversation owner accepted")
	}
}
func TestNativeEvidenceDoesNotWriteCheckpointAndAdmissionIsImmutableAcrossRestart(t *testing.T) {
	s, f, ctx, a := nativeTestStore(t)
	if e := s.SaveAdmission(ctx, a, "worker"); e != nil {
		t.Fatal(e)
	}
	var checkpoint map[string]any
	_ = json.Unmarshal([]byte(*f.run.CheckpointData), &checkpoint)
	if checkpoint["unrelated"].(map[string]any)["retain"] != true {
		t.Fatal("clobbered other checkpoint")
	}
	restarted := NewNativeSourceStore(f, f, f)
	loaded, e := restarted.LoadAdmission(ctx, a.Scope)
	if e != nil || loaded.StarterMessageID != a.StarterMessageID {
		t.Fatal("restart admission", e)
	}
	changed := a
	changed.StarterMessageID = "different"
	before := *f.run.CheckpointData
	if e = s.SaveAdmission(ctx, changed, "worker"); e == nil {
		t.Fatal("immutable admission replaced")
	}
	if *f.run.CheckpointData != before {
		t.Fatal("changed rejected checkpoint")
	}
	p, e := NewPlan(ctx, ProjectionPolicyProducer{}, a, actualSources(t))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SavePlan(ctx, p, "worker"); e != nil {
		t.Fatal(e)
	}
	if _, e = restarted.LoadPlan(ctx, a.Scope, p.ID); e != nil {
		t.Fatal(e)
	}
	f.loseLease = true
	if e = s.SavePlan(ctx, p, "worker"); e == nil {
		t.Fatal("lost lease checkpoint reported success")
	}
}

func (f *nativeFixture) PatchMessage(_ context.Context, m *apiconv.MutableMessage) error {
	for _, turn := range f.conversation.GetTranscript() {
		for _, item := range turn.Message {
			if item.Id == m.Id {
				item.Content = m.Content
				return nil
			}
		}
	}
	return reject("message not found")
}
func (f *nativeFixture) GetMessage(_ context.Context, id string, _ ...apiconv.Option) (*apiconv.Message, error) {
	for _, turn := range f.conversation.GetTranscript() {
		for _, item := range turn.Message {
			if item.Id == id {
				copy := apiconv.Message(*item)
				return &copy, nil
			}
		}
	}
	return nil, reject("message not found")
}
func TestNativeReceiptProjectionPreservesOriginalPayloadAndRestartHistory(t *testing.T) {
	s, f, ctx, a := nativeTestStore(t)
	if e := s.SaveAdmission(ctx, a, "worker"); e != nil {
		t.Fatal(e)
	}
	p, e := NewPlan(ctx, ProjectionPolicyProducer{}, a, actualSources(t))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SavePlan(ctx, p, "worker"); e != nil {
		t.Fatal(e)
	}
	fixture := loadFixture(t)
	op := firstCall(&fixture).OpID
	before, e := s.LoadCompletedCall(ctx, a.Scope, op)
	if e != nil {
		t.Fatal(e)
	}
	runtime, _ := NewRuntime(ProjectionPolicyProducer{}, s)
	body, e := runtime.DecorateAndPublishCompleted(ctx, a.Scope, p.ID, op)
	if e != nil {
		t.Fatal(e)
	}
	restarted := NewNativeSourceStore(f, f, f)
	after, e := restarted.LoadCompletedCall(ctx, a.Scope, op)
	if e != nil {
		t.Fatal(e)
	}
	if string(before.Response) != string(after.Response) {
		t.Fatal("rewrote immutable source payload")
	}
	message, e := f.GetMessage(ctx, after.MessageID)
	if e != nil {
		t.Fatal(e)
	}
	if message.GetContentPreferContent() != string(body) {
		t.Fatal("receipt absent from resumed model history")
	}
}
