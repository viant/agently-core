package forecastbinding

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func actualSources(t *testing.T) PlanSources {
	t.Helper()
	b, e := os.ReadFile("testdata/actual_profile_conversion_projection.json")
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Profile    Call `json:"profile"`
		Conversion Call `json:"conversion"`
	}
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	return PlanSources{Profile: v.Profile, Conversion: v.Conversion}
}
func actualAdmission(t *testing.T) Admission {
	f := loadFixture(t)
	return Admission{Scope: f.Scope, StarterMessageID: "starter", ReceivedAt: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), TimeZone: "UTC", SelectionOrigin: SelectionUser, AudienceIDs: []int64{7366798}, DateOrigin: DateUserWindow, Dates: f.Policy.Dates}
}
func TestProductionProjectionPlanAndExplicitAdmission(t *testing.T) {
	a := actualAdmission(t)
	sources := actualSources(t)
	p, e := NewPlan(context.Background(), ProjectionPolicyProducer{}, a, sources)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.ValidateIdentity(); e != nil {
		t.Fatal(e)
	}
	f := loadFixture(t)
	got, e := Materialize(a.Scope, p.Produced.Policy, f.Bindings, f.Records)
	if e != nil {
		t.Fatal(e)
	}
	want, _ := canonical(f.Expected)
	if string(got.Data) != string(want) {
		t.Fatalf("wrong projected rows %s", got.Data)
	}
	a.AudienceIDs = []int64{7366799}
	if _, e = NewPlan(context.Background(), ProjectionPolicyProducer{}, a, sources); e == nil {
		t.Fatal("selection mismatch downgraded")
	}
	a = actualAdmission(t)
	a.Dates = []string{"2026-10-01"}
	if _, e = NewPlan(context.Background(), ProjectionPolicyProducer{}, a, sources); e == nil {
		t.Fatal("date mismatch downgraded")
	}
	a = actualAdmission(t)
	a.SelectionOrigin = SelectionToolEvidence
	a.AudienceIDs = nil
	a.DateOrigin = DateToolEvidence
	a.Dates = nil
	p, e = NewPlan(context.Background(), ProjectionPolicyProducer{}, a, sources)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Produced.Policy.Dates) != 3 || p.Admission.DateOrigin != DateToolEvidence {
		t.Fatal("missing truthful fallback provenance")
	}
}

type receiptStore struct {
	admission Admission
	plan      *Plan
	calls     map[string]Call
	fail      bool
}

func (s *receiptStore) LoadAdmission(context.Context, Scope) (*Admission, error) {
	return &s.admission, nil
}
func (s *receiptStore) LoadPlan(_ context.Context, scope Scope, id string) (*Plan, error) {
	if s.fail {
		return nil, reject("store failed")
	}
	if s.plan == nil || s.plan.ID != id || s.plan.Admission.Scope != scope {
		return nil, reject("plan missing")
	}
	return s.plan, nil
}
func (s *receiptStore) SavePlan(_ context.Context, p *Plan, _ string) error { s.plan = p; return nil }
func (s *receiptStore) LoadCompletedCall(_ context.Context, scope Scope, id string) (*Call, error) {
	c, ok := s.calls[id]
	if !ok || c.Scope != scope || c.Status != "completed" {
		return nil, reject("not durably completed")
	}
	return &c, nil
}
func TestReceiptsReleaseOnlyAfterDurableCompletionAndSurviveRuntimeRestart(t *testing.T) {
	f := loadFixture(t)
	a := actualAdmission(t)
	sources := actualSources(t)
	p, e := NewPlan(context.Background(), ProjectionPolicyProducer{}, a, sources)
	if e != nil {
		t.Fatal(e)
	}
	store := &receiptStore{admission: a, plan: p, calls: map[string]Call{}}
	runtime, e := NewRuntime(ProjectionPolicyProducer{}, store)
	if e != nil {
		t.Fatal(e)
	}
	c := *firstCall(&f)
	c.Status = "running"
	store.calls[c.OpID] = c
	if _, e = runtime.DecorateCompleted(context.Background(), a.Scope, p.ID, c.OpID); e == nil {
		t.Fatal("released receipt before durable completion")
	}
	c.Status = "completed"
	store.calls[c.OpID] = c
	first, e := runtime.DecorateCompleted(context.Background(), a.Scope, p.ID, c.OpID)
	if e != nil {
		t.Fatal(e)
	}
	restarted, _ := NewRuntime(ProjectionPolicyProducer{}, store)
	second, e := restarted.DecorateCompleted(context.Background(), a.Scope, p.ID, c.OpID)
	if e != nil || string(first) != string(second) {
		t.Fatal("restart changed receipt", e)
	}
	duplicate := c
	duplicate.OpID = "coalesced-new-operation"
	store.calls[duplicate.OpID] = duplicate
	other, e := runtime.DecorateCompleted(context.Background(), a.Scope, p.ID, duplicate.OpID)
	if e != nil {
		t.Fatal(e)
	}
	var receipt map[string]any
	_ = json.Unmarshal(other, &receipt)
	if receipt[ReceiptKey].(map[string]any)["opId"] != duplicate.OpID {
		t.Fatal("coalesced receipt used original op")
	}
	store.fail = true
	if _, e = runtime.DecorateCompleted(context.Background(), a.Scope, p.ID, c.OpID); e == nil {
		t.Fatal("released unconfirmed receipt")
	}
	store.fail = false
	c.Response = json.RawMessage(`{"status":"ok","data":[{"avails":1}],"continuation":{"hasMore":true,"remaining":2}}`)
	store.calls[c.OpID] = c
	if _, e = runtime.DecorateCompleted(context.Background(), a.Scope, p.ID, c.OpID); e == nil {
		t.Fatal("partial overflow acquired receipt")
	}
	c.Response = first
	store.calls[c.OpID] = c
	if _, e = runtime.DecorateCompleted(context.Background(), a.Scope, p.ID, c.OpID); e == nil {
		t.Fatal("reserved receipt collision accepted")
	}
}

func TestPrepareConversionUsesLoadedProfileAndAdvertisesStableRequestHashes(t *testing.T) {
	a := actualAdmission(t)
	sources := actualSources(t)
	store := &receiptStore{admission: a, calls: map[string]Call{sources.Profile.OpID: sources.Profile, sources.Conversion.OpID: sources.Conversion}}
	runtime, _ := NewRuntime(ProjectionPolicyProducer{}, store)
	original := json.RawMessage(`{"Request":{"from":"2026-10-02","to":"2026-10-04"},"timeoutMs":600000}`)
	effective, e := runtime.PrepareConversion(context.Background(), a.Scope, sources.Profile.OpID, original)
	if e != nil {
		t.Fatal(e)
	}
	request, _ := object(effective)
	args := request["Request"].(map[string]any)
	if args["evidenceProfile"] == nil || args["inclusion"] == nil {
		t.Fatal("missing source-derived conversion fields")
	}
	if string(original) != `{"Request":{"from":"2026-10-02","to":"2026-10-04"},"timeoutMs":600000}` {
		t.Fatal("rewrote original request")
	}
	if _, e = runtime.PrepareConversion(context.Background(), a.Scope, sources.Profile.OpID, json.RawMessage(`{"Request":{"inclusion":"other","from":"2026-10-02","to":"2026-10-04"}}`)); e == nil {
		t.Fatal("explicit targeting mismatch discarded")
	}
	if _, e = runtime.PrepareConversion(context.Background(), a.Scope, sources.Profile.OpID, json.RawMessage(`{"Request":{"evidenceProfile":{}}}`)); e == nil {
		t.Fatal("model supplied trusted profile")
	}
	p, e := runtime.AdmitPlan(context.Background(), a.Scope, sources.Profile.OpID, sources.Conversion.OpID, "worker")
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := runtime.AdvertisePlan(context.Background(), a.Scope, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(receipt.Expected) != 15 {
		t.Fatal("expected complete15-request receipt")
	}
	for _, r := range receipt.Expected {
		h, _ := RequestHash(r.Request)
		if h != r.RequestHash {
			t.Fatal("advertised wrong request hash")
		}
	}
}
