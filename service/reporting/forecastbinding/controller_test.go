package forecastbinding

import (
	"context"
	"encoding/json"
	"testing"

	authctx "github.com/viant/agently-core/internal/auth"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

type controllerStore struct {
	*receiptStore
	ids       []string
	projected map[string]json.RawMessage
}

func (s *controllerStore) CompletedOperations(context.Context, Scope, string) ([]string, error) {
	return s.ids, nil
}
func (s *controllerStore) PublishCompletedProjection(_ context.Context, _ Scope, id string, body json.RawMessage) error {
	s.projected[id] = append(json.RawMessage(nil), body...)
	return nil
}

func TestControllerConversionUsesOneExactProfileAndDurablePlan(t *testing.T) {
	a := actualAdmission(t)
	sources := actualSources(t)
	store := &controllerStore{receiptStore: &receiptStore{admission: a, calls: map[string]Call{sources.Profile.OpID: sources.Profile}}, ids: []string{sources.Profile.OpID}, projected: map[string]json.RawMessage{}}
	runtime, _ := NewRuntime(ProjectionPolicyProducer{}, store)
	controller, _ := NewController(runtime, a.Scope, func() string { return "current-lease" })
	ctx := controllerContext(a.Scope)
	raw := json.RawMessage(`{"Request":{"from":"2026-10-02","to":"2026-10-04"}}`)
	effective, handled, err := controller.Prepare(ctx, "steward/ForecastingTargetingConvert", "convert", raw)
	if err != nil || !handled {
		t.Fatal(err)
	}
	request, _ := object(effective)
	args := request["Request"].(map[string]any)
	if args[trustedProfileReference] != sources.Profile.OpID || args["evidenceProfile"] == nil {
		t.Fatal("missing captured profile provenance")
	}
	sources.Conversion.Request = effective
	store.calls[sources.Conversion.OpID] = sources.Conversion
	body, handled, err := controller.Completed(ctx, "steward/ForecastingTargetingConvert", sources.Conversion.OpID)
	if err != nil || !handled {
		t.Fatal(err)
	}
	projection, _ := object(body)
	if projection[PlanReceiptKey] == nil || store.plan == nil || len(store.projected) != 1 {
		t.Fatal("plan not durably projected")
	}
	restarted, _ := NewController(runtime, a.Scope, func() string { return "new-lease" })
	again, _, err := restarted.Completed(ctx, "steward/ForecastingTargetingConvert", sources.Conversion.OpID)
	if err != nil || string(again) != string(body) {
		t.Fatal("restart changed immutable plan receipt", err)
	}
	duplicate := sources.Profile
	duplicate.OpID = "another-matching-profile"
	store.calls[duplicate.OpID] = duplicate
	store.ids = append(store.ids, duplicate.OpID)
	if _, _, err = controller.Prepare(ctx, "steward/ForecastingTargetingConvert", "ambiguous", raw); err == nil {
		t.Fatal("selected profile by order")
	}
	explicit := json.RawMessage(`{"Request":{"from":"2026-10-02","to":"2026-10-04","sourceProfileOpId":"` + sources.Profile.OpID + `"}}`)
	if _, _, err = controller.Prepare(ctx, "steward/ForecastingTargetingConvert", "explicit", explicit); err != nil {
		t.Fatal(err)
	}
	foreign := a.Scope
	foreign.TurnID = "foreign"
	if _, _, err = controller.Prepare(controllerContext(foreign), "steward/ForecastingTargetingConvert", "foreign", raw); err == nil {
		t.Fatal("foreign turn inherited controller")
	}
	if _, _, err = controller.Prepare(ctx, "steward/ForecastingTargetingConvert", "spoof", effective); err == nil {
		t.Fatal("trusted source reinjection accepted")
	}
}

func controllerContext(scope Scope) context.Context {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: scope.OwnerID})
	return requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: scope.ConversationID, TurnID: scope.TurnID})
}

func TestActualSourceReceiptsCarryExecutedDatesWithoutPolicyClaims(t *testing.T) {
	fixture := loadFixture(t)
	for _, call := range fixture.Records {
		body, err := sourceProjection(&call)
		if err != nil {
			t.Fatal(err)
		}
		value, _ := object(body)
		receipt := value[SourceReceiptKey].(map[string]any)
		if receipt["opId"] != call.OpID || receipt["date"] == nil || receipt["planId"] != nil || receipt["selectionOrigin"] != nil {
			t.Fatal("source receipt invents policy", receipt)
		}
	}
}
