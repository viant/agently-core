package overlay_test

import (
	"sort"
	"testing"

	loproto "github.com/viant/agently-core/protocol/lookup/overlay"
	"github.com/viant/agently-core/service/lookup/overlay"
)

// schema5 mirrors the fixture in lookups-test.mjs: 5-field schema as might
// come from an MCP-generated tool input or an LLM-authored elicitation.
func schema5() map[string]interface{} {
	return map[string]interface{}{
		"account_id":  map[string]interface{}{"type": "integer"},
		"project_id":  map[string]interface{}{"type": "integer"},
		"feature_key": map[string]interface{}{"type": "string"},
		"start_date":  map[string]interface{}{"type": "string", "format": "date"},
		"note":        map[string]interface{}{"type": "string"},
	}
}

func ov(id string, mode loproto.Mode, pri int, bindings ...loproto.Binding) *loproto.Overlay {
	return &loproto.Overlay{
		ID:       id,
		Priority: pri,
		Target:   loproto.Target{Kind: "template", ID: "any"},
		Mode:     mode,
		Bindings: bindings,
	}
}

func fieldBinding(ds, fieldName, wantType string) loproto.Binding {
	return loproto.Binding{
		Match:  loproto.Match{FieldName: fieldName, Type: wantType},
		Lookup: loproto.Lookup{DataSource: ds},
	}
}

func apply(t *testing.T, overlays []*loproto.Overlay) map[string]string {
	t.Helper()
	store := overlay.NewMemoryStore()
	store.Replace(overlays)
	svc := overlay.New(store)
	props := schema5()
	svc.Apply("template", "any", props)
	return extractAttachedDataSources(props)
}

func extractAttachedDataSources(schemaProps map[string]interface{}) map[string]string {
	out := make(map[string]string)
	for k, v := range schemaProps {
		prop, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if w, _ := prop["x-ui-widget"].(string); w != "lookup" {
			continue
		}
		att, _ := prop["x-ui-lookup"].(map[string]interface{})
		if ds, _ := att["dataSource"].(string); ds != "" {
			out[k] = ds
		}
	}
	return out
}

// T18 — Partial mode: library overlays attach to matching fields only.
func TestApply_PartialLibraryOverlays(t *testing.T) {
	ovA := ov("fields.account_id", loproto.ModePartial, 10, fieldBinding("account", "account_id", "integer"))
	ovC := ov("fields.project_id", loproto.ModePartial, 10, fieldBinding("project", "project_id", "integer"))
	ovF := ov("fields.feature_key", loproto.ModePartial, 10, fieldBinding("system_feature", "feature_key", ""))
	got := apply(t, []*loproto.Overlay{ovA, ovC, ovF})

	want := map[string]string{
		"account_id":  "account",
		"project_id":  "project",
		"feature_key": "system_feature",
	}
	mustEqual(t, got, want)
}

// T19 — Strict mode: one unmatched binding discards the whole overlay.
func TestApply_StrictDiscardsWhenAnyBindingUnmatched(t *testing.T) {
	strictOV := ov("template.strict", loproto.ModeStrict, 100,
		fieldBinding("account-premium", "account_id", "integer"),
		fieldBinding("project-premium", "project_id", "integer"),
		fieldBinding("never", "missing_field", ""),
	)
	got := apply(t, []*loproto.Overlay{strictOV})
	if len(got) != 0 {
		t.Fatalf("strict overlay with unmatched binding must apply nothing, got %v", got)
	}
}

// T20 — Strict+high-priority overrides partial library overlays on collisions;
// untouched fields still pick up the library overlay.
func TestApply_PriorityCompositionAcrossOverlays(t *testing.T) {
	library := []*loproto.Overlay{
		ov("fields.account_id", loproto.ModePartial, 10, fieldBinding("account", "account_id", "integer")),
		ov("fields.project_id", loproto.ModePartial, 10, fieldBinding("project", "project_id", "integer")),
		ov("fields.feature_key", loproto.ModePartial, 10, fieldBinding("system_feature", "feature_key", "")),
	}
	// Strict override matching two fields (no unmatched bindings).
	override := ov("template.ok", loproto.ModeStrict, 100,
		fieldBinding("account-premium", "account_id", "integer"),
		fieldBinding("project-premium", "project_id", "integer"),
	)
	got := apply(t, append(library, override))
	want := map[string]string{
		"account_id":  "account-premium", // priority 100 > 10
		"project_id":  "project-premium", // priority 100 > 10
		"feature_key": "system_feature",  // no override → library wins
	}
	mustEqual(t, got, want)
}

// T21 — Threshold: applies when ≥N bindings match.
func TestApply_ThresholdSatisfied(t *testing.T) {
	threshold := &loproto.Overlay{
		ID: "pattern.ids_like", Priority: 5,
		Target: loproto.Target{Kind: "template", ID: "any"},
		Mode:   loproto.ModeThreshold, Threshold: 2,
		Bindings: []loproto.Binding{
			{Match: loproto.Match{FieldNameRegex: `^.*_id$`, Type: "integer"},
				Lookup: loproto.Lookup{DataSource: "generic_id_picker"}},
			{Match: loproto.Match{FieldNameRegex: `^.*_key$`},
				Lookup: loproto.Lookup{DataSource: "generic_key_picker"}},
		},
	}
	got := apply(t, []*loproto.Overlay{threshold})
	want := map[string]string{
		"account_id":  "generic_id_picker",
		"project_id":  "generic_id_picker",
		"feature_key": "generic_key_picker",
	}
	mustEqual(t, got, want)
}

// T22 — Threshold not satisfied: whole overlay discarded.
func TestApply_ThresholdDiscardsWhenBelow(t *testing.T) {
	// Trim schema so only one binding matches; threshold still 2.
	store := overlay.NewMemoryStore()
	store.Replace([]*loproto.Overlay{{
		ID: "pattern.ids_like", Priority: 5,
		Target: loproto.Target{Kind: "template", ID: "any"},
		Mode:   loproto.ModeThreshold, Threshold: 2,
		Bindings: []loproto.Binding{
			{Match: loproto.Match{FieldNameRegex: `^.*_id$`, Type: "integer"},
				Lookup: loproto.Lookup{DataSource: "generic_id_picker"}},
			{Match: loproto.Match{FieldNameRegex: `^.*_key$`},
				Lookup: loproto.Lookup{DataSource: "generic_key_picker"}},
		},
	}})
	svc := overlay.New(store)
	props := map[string]interface{}{
		"account_id": map[string]interface{}{"type": "integer"},
	}
	svc.Apply("template", "any", props)
	got := extractAttachedDataSources(props)
	if len(got) != 0 {
		t.Fatalf("below-threshold overlay must apply nothing, got %v", got)
	}
}

// T23 — Each overlay evaluates its own mode in isolation.
func TestApply_PerOverlayModeIsolation(t *testing.T) {
	store := overlay.NewMemoryStore()
	store.Replace([]*loproto.Overlay{
		ov("fields.account_id", loproto.ModePartial, 10, fieldBinding("account", "account_id", "integer")),
		{
			ID: "pattern.ids_like", Priority: 5,
			Target: loproto.Target{Kind: "template", ID: "any"},
			Mode:   loproto.ModeThreshold, Threshold: 2,
			Bindings: []loproto.Binding{
				{Match: loproto.Match{FieldNameRegex: `^.*_id$`, Type: "integer"},
					Lookup: loproto.Lookup{DataSource: "generic_id_picker"}},
				{Match: loproto.Match{FieldNameRegex: `^.*_key$`},
					Lookup: loproto.Lookup{DataSource: "generic_key_picker"}},
			},
		},
	})
	svc := overlay.New(store)
	props := map[string]interface{}{
		"account_id": map[string]interface{}{"type": "integer"},
	}
	svc.Apply("template", "any", props)
	got := extractAttachedDataSources(props)
	// Partial kept its hit; threshold discarded entirely.
	want := map[string]string{"account_id": "account"}
	mustEqual(t, got, want)
}

// T24 — Multi individual-field overlays compose (1-of-N × M).
func TestApply_MultiSingleFieldOverlaysCompose(t *testing.T) {
	names := []string{
		"account_id", "project_id", "feature_key",
		"foo", "bar", "baz", "qux", "quux", "corge", "grault",
	}
	lib := make([]*loproto.Overlay, 0, len(names))
	for _, n := range names {
		lib = append(lib, ov("fields."+n, loproto.ModePartial, 10,
			loproto.Binding{
				Match:  loproto.Match{FieldName: n},
				Lookup: loproto.Lookup{DataSource: "ds_" + n},
			}))
	}
	got := apply(t, lib)
	want := map[string]string{
		"account_id":  "ds_account_id",
		"project_id":  "ds_project_id",
		"feature_key": "ds_feature_key",
	}
	mustEqual(t, got, want)
}

// T_Registry — named-token bindings compose into the registry endpoint.
func TestRegistry_NamedTokensCompose(t *testing.T) {
	store := overlay.NewMemoryStore()
	store.Replace([]*loproto.Overlay{
		{
			ID:       "named.account",
			Priority: 10,
			Target:   loproto.Target{Kind: "template", ID: "any"},
			Bindings: []loproto.Binding{
				{
					Named: &loproto.NamedToken{
						Name:         "account",
						Required:     true,
						QueryInput:   "q",
						ResolveInput: "id",
						Store:        "${id}",
						ModelForm:    "${id}",
					},
					Lookup: loproto.Lookup{DataSource: "account", Display: "${workItemName}"},
				},
			},
		},
		{
			ID:       "named.window",
			Priority: 5,
			Target:   loproto.Target{Kind: "template", ID: "any"},
			Bindings: []loproto.Binding{
				{
					Named:  &loproto.NamedToken{Name: "window"},
					Lookup: loproto.Lookup{DataSource: "time_windows"},
				},
			},
		},
	})
	svc := overlay.New(store)
	entries := svc.Registry("template", "any")
	if len(entries) != 2 {
		t.Fatalf("want 2 registry entries, got %d", len(entries))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	if entries[0].Name != "account" || entries[0].DataSource != "account" {
		t.Fatalf("entry 0 mismatch: %+v", entries[0])
	}
	if entries[1].Name != "window" || entries[1].DataSource != "time_windows" {
		t.Fatalf("entry 1 mismatch: %+v", entries[1])
	}
	if !entries[0].Required || entries[0].Token == nil || entries[0].Token.ModelForm != "${id}" {
		t.Fatalf("account entry missing token/required: %+v", entries[0])
	}
	if entries[0].Token.QueryInput != "q" || entries[0].Token.ResolveInput != "id" {
		t.Fatalf("account entry missing query/resolve inputs: %+v", entries[0].Token)
	}
	if entries[0].Token.Display != "${workItemName}" {
		t.Fatalf("account entry missing inherited display: %+v", entries[0].Token)
	}
	// Default trigger.
	if entries[0].Trigger != "/" {
		t.Fatalf("want default trigger /, got %q", entries[0].Trigger)
	}
}

func mustEqual(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("attachments length mismatch: got %v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("attachment %s: got %q want %q", k, got[k], v)
		}
	}
}
