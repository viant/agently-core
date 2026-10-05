package forecastbinding

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/viant/agently-core/runtime/evidence"
)

type lifecycleStore struct {
	*controllerStore
	saves int
	lease string
}

func (s *lifecycleStore) SaveAdmission(_ context.Context, a Admission, lease string) error {
	if lease == "" {
		return reject("lease missing")
	}
	if s.saves > 0 {
		return reject("duplicate admission write")
	}
	s.saves++
	s.lease = lease
	raw, _ := json.Marshal(a)
	return json.Unmarshal(raw, &s.admission)
}

func TestLifecycleCapturesIntentBeforeRoutingAndRestoresOriginalAdmission(t *testing.T) {
	a := actualAdmission(t)
	store := &lifecycleStore{controllerStore: &controllerStore{receiptStore: &receiptStore{calls: map[string]Call{}}, projected: map[string]json.RawMessage{}}}
	factory, err := NewFactory(ProjectionPolicyProducer{}, store, "America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	ctx := controllerContext(a.Scope)
	raw := json.RawMessage(`{"client":{"forecastIntent":{"version":1,"selectedEntities":[{"kind":"audience","id":"7366798"}],"window":{"mode":"workspaceDefault"}}},"scope":{"audienceId":42}}`)
	capturedAt := time.Date(2026, 10, 4, 18, 0, 0, 0, time.UTC)
	pending, err := factory.Capture(ctx, evidence.Input{Context: raw, ReceivedAt: capturedAt})
	if err != nil {
		t.Fatal(err)
	}
	// An intake sidecar may replace all visible context after capture.
	for i := range raw {
		raw[i] = ' '
	}
	owner := "lease-after-heartbeat"
	turn := evidence.Turn{ConversationID: a.ConversationID, TurnID: a.TurnID, StarterMessageID: "durable-starter", LeaseOwner: func() string { return owner }}
	installed, err := pending.Begin(ctx, turn)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.ToolsFromContext(installed) == nil || store.saves != 1 || store.lease != owner || store.admission.AudienceIDs[0] != 7366798 || !store.admission.ReceivedAt.Equal(capturedAt) {
		t.Fatal("lost captured intent or current lease")
	}
	if store.admission.Dates[0] != "2026-10-02" || store.admission.DateOrigin != DateWorkspaceDefault {
		t.Fatal("wrong captured clock window")
	}
	original, _ := json.Marshal(store.admission)
	restarted, err := NewFactory(ProjectionPolicyProducer{}, store, "Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	owner = "new-worker-lease"
	restored, err := restarted.Restore(ctx, turn)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(store.admission)
	if string(original) != string(after) || store.saves != 1 || evidence.ToolsFromContext(restored) == nil {
		t.Fatal("resume recaptured clock or intent")
	}
}

func TestLifecycleDoesNotPromoteNestedContextOrAllowPrincipalChanges(t *testing.T) {
	a := actualAdmission(t)
	store := &lifecycleStore{controllerStore: &controllerStore{receiptStore: &receiptStore{}, projected: map[string]json.RawMessage{}}}
	factory, _ := NewFactory(ProjectionPolicyProducer{}, store, "UTC")
	ctx := controllerContext(a.Scope)
	raw := json.RawMessage(`{"client":{"forecastIntent":{"version":1,"selectedEntities":[{"kind":"audience","id":"7366798"}]}}}`)
	pending, err := factory.Capture(ctx, evidence.Input{Context: raw, ReceivedAt: a.ReceivedAt, Nested: true})
	if err != nil {
		t.Fatal(err)
	}
	turn := evidence.Turn{ConversationID: a.ConversationID, TurnID: a.TurnID, StarterMessageID: "starter", LeaseOwner: func() string { return "lease" }}
	foreign := a.Scope
	foreign.OwnerID = "foreign"
	if _, err = pending.Begin(controllerContext(foreign), turn); err == nil || store.saves != 0 {
		t.Fatal("changed principal wrote admission")
	}
	if _, err = pending.Begin(ctx, turn); err != nil {
		t.Fatal(err)
	}
	if store.admission.SelectionOrigin != SelectionToolEvidence || len(store.admission.AudienceIDs) != 0 {
		t.Fatal("nested model context became user intent")
	}
}
