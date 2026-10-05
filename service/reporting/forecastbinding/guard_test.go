package forecastbinding

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func guardFixture(t *testing.T) (*Guard, string) {
	t.Helper()
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
	guard := newGuard(runtime, admission.Scope, func(context.Context) error { return nil }, func() bool { return true })
	binding := DataBindings{PlanID: plan.ID, Profile: fixture.Bindings.Profile, Columns: fixture.Bindings.Columns}
	data, _ := json.Marshal(map[string]any{"version": 2, "id": "timeline", "reportRef": "forecast", "sequence": 2, "mode": "replace", "format": "json", "sourceBindings": binding})
	start := `{"version":1,"id":"forecast","sequence":1,"mode":"start","grammar":"report-document-v1","blocks":[{"id":"table","kind":"tableBlock","datasetRef":"timeline","columns":[{"key":"date"},{"key":"overall"}]}]}`
	commit := `{"version":1,"id":"forecast","sequence":3,"mode":"commit"}`
	return guard, "Prose remains.\n```forge-report\n" + start + "\n```\n```forge-data\n" + string(data) + "\n```\n```forge-report\n" + commit + "\n```\n"
}

func TestGuardAllSplitPositionsBindBeforePublishingAndReplayFinal(t *testing.T) {
	_, input := guardFixture(t)
	// Boundary splits include every character of opening/closing markers and a
	// payload split. The separate stream primitive covers all byte offsets/UTF-8.
	splits := []int{0, 1, 17, 18, 19, 20, 21, 22, len(input) / 2, len(input) - 1, len(input)}
	for _, split := range splits {
		guard, _ := guardFixture(t)
		a, err := guard.Stream(context.Background(), "message", input[:split], false)
		if err != nil {
			t.Fatal(split, err)
		}
		b, err := guard.Stream(context.Background(), "message", input[split:], true)
		if err != nil {
			t.Fatal(split, err)
		}
		visible := a + b
		if strings.Contains(visible, "sourceBindings") || !strings.Contains(visible, "397765284") {
			t.Fatal("binding not consumed or rows not materialized")
		}
		final, err := guard.Content(context.Background(), input)
		if err != nil {
			t.Fatal("final replay", err)
		}
		if final != visible {
			t.Fatal("final and stream differ")
		}
		// An exact cleaned projection is idempotent, but no other unbound rows gain authority.
		if _, err = guard.Content(context.Background(), visible); err != nil {
			t.Fatal("canonical replay", err)
		}
	}
}

func TestGuardRejectsRenamedMixedAndInlineDatasets(t *testing.T) {
	ctx := context.Background()
	for _, body := range []string{
		`{"version":2,"id":"renamed","reportRef":"forecast","sequence":2,"data":[{"date":"2026-10-02","overall":1}]}`,
		`{"version":2,"id":"unrelated-table","reportRef":"other","sequence":2,"profile":"not-forecast","data":[{"revenue":1}]}`,
	} {
		guard, _ := guardFixture(t)
		if _, err := guard.Fence(ctx, "forge-data", body); err == nil {
			t.Fatal("model label exempted dataset")
		}
	}
	for _, body := range []string{
		`{"blocks":[{"kind":"tableBlock","rows":[{"avails":1}]}]}`,
		`{"blocks":[{"kind":"chartBlock","chartModel":{"series":[{"data":[1,2]}]}}]}`,
		`{"blocks":[{"kind":"chartBlock","chartModel":{"series":{"values":[1,2]}}}]}`,
		`{"blocks":[{"kind":"kpiBlock","value":123}]}`,
	} {
		guard, _ := guardFixture(t)
		if _, err := guard.Fence(ctx, "forge-report", body); err == nil {
			t.Fatal("inline bypass", body)
		}
	}
	guard, _ := guardFixture(t)
	if _, err := guard.Fence(ctx, "forge-config", `{"type":"Table","rows":[1]}`); err == nil {
		t.Fatal("config bypass")
	}
}

func TestGuardRejectsMissingDatasetCommitAndPreservesInactiveTurns(t *testing.T) {
	guard, input := guardFixture(t)
	start := `{"version":1,"id":"forecast","sequence":1,"mode":"start","blocks":[{"id":"chart","kind":"chartBlock","datasetRef":"not-bound"}]}`
	if _, err := guard.Fence(context.Background(), "forge-report", start); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Fence(context.Background(), "forge-report", `{"version":1,"id":"forecast","sequence":2,"mode":"commit"}`); err == nil {
		t.Fatal("unbound chart committed")
	}
	guard.active = func() bool { return false }
	if got, err := guard.Content(context.Background(), input); err != nil || got != input {
		t.Fatal("unrelated turn changed", err)
	}
}
