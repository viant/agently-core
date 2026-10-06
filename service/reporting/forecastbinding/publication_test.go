package forecastbinding

import (
	"context"
	"encoding/json"
	"testing"
)

func TestBoundEnvelopeMaterializesActualFifteenCellsAndRejectsSwappedRows(t *testing.T) {
	fixture := loadFixture(t)
	admission := actualAdmission(t)
	plan, err := NewPlan(context.Background(), ProjectionPolicyProducer{}, admission, actualSources(t))
	if err != nil {
		t.Fatal(err)
	}
	store := &receiptStore{admission: admission, plan: plan, calls: map[string]Call{}}
	for _, call := range fixture.Records {
		store.calls[call.OpID] = call
	}
	runtime, _ := NewRuntime(ProjectionPolicyProducer{}, store)
	binding := DataBindings{PlanID: plan.ID, Profile: fixture.Bindings.Profile, Columns: fixture.Bindings.Columns}
	envelope := map[string]any{"version": 1, "scope": "forecast", "id": "timeline", "reportRef": "forecast-report", "sequence": 2, "mode": "snapshot", "format": "json", "sourceBindings": binding}
	raw, _ := json.Marshal(envelope)
	body, err := runtime.BindDataEnvelope(context.Background(), admission.Scope, raw)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err = json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	got, _ := canonical(result["data"])
	want, _ := canonical(fixture.Expected)
	if string(got) != string(want) || result["sourceBindings"] != nil {
		t.Fatal("did not produce canonical correct data")
	}
	envelope["data"] = fixture.Expected
	raw, _ = json.Marshal(envelope)
	checked, err := runtime.BindDataEnvelope(context.Background(), admission.Scope, raw)
	if err != nil || string(checked) != string(body) {
		t.Fatal("correct authored data changed", err)
	}
	envelope["data"] = fixture.Incorrect
	raw, _ = json.Marshal(envelope)
	if _, err = runtime.BindDataEnvelope(context.Background(), admission.Scope, raw); err == nil {
		t.Fatal("accepted Oct2/3 swap")
	}
	delete(envelope, "data")
	delete(envelope, "sourceBindings")
	raw, _ = json.Marshal(envelope)
	if _, err = runtime.BindDataEnvelope(context.Background(), admission.Scope, raw); err == nil {
		t.Fatal("accepted missing bindings")
	}
	envelope["sourceBindings"] = binding
	raw, _ = json.Marshal(envelope)
	foreign := admission.Scope
	foreign.TurnID = "foreign"
	if _, err = runtime.BindDataEnvelope(context.Background(), foreign, raw); err == nil {
		t.Fatal("accepted foreign plan")
	}
}
