package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/tiendc/go-deepcopy"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/ui/permittedview"
	windowloader "github.com/viant/agently-core/service/ui/window"
	"github.com/viant/forge/backend/types"
	"os"
	"runtime"
	"testing"
	"time"
)

type profilePresentationFacts struct{ snapshot *permittedview.Snapshot }

func (f profilePresentationFacts) Resolve(context.Context, *permittedview.Request) (*permittedview.Snapshot, error) {
	return f.snapshot, nil
}
func (f profilePresentationFacts) ResolvePresentation(context.Context, *permittedview.BoundView) (*permittedview.Snapshot, error) {
	return f.snapshot, nil
}

// Opt-in readonly preparation profile using the actual nested advertiser assets.
// Source capture occurs once; phase samples exclude startup/filesystem work and
// UI-facts network latency. There is no production cache/clone optimization here.
func TestOriginalAdvertiserWindowPreparationProfile(t *testing.T) {
	root := os.Getenv("AGENTLY_NATIVE_SOURCE_BENCH_ROOT")
	if root == "" {
		t.Skip("opt-in original assets preparation profile")
	}
	ctx := context.Background()
	before := stewardAssetDigest(t, root)
	defer func() { require.Equal(t, before, stewardAssetDigest(t, root)) }()
	definitions, err := SnapshotWindowDefinitions(ctx, root, []windowloader.ResourceBinding{{WindowKey: "advertiser", URI: "window://workspace/advertiser"}}, nil)
	require.NoError(t, err)
	snapshot, err := NewNativeAssetSnapshot(ctx, root, definitions)
	require.NoError(t, err)
	uri, err := identity.ParseResourceURI("window://workspace/advertiser")
	require.NoError(t, err)
	candidates, err := snapshot.Candidates(ctx, uri)
	require.NoError(t, err)
	raw, err := snapshot.ReadCandidate(ctx, uri, candidates[0])
	require.NoError(t, err)
	compiled, err := types.CompileWindowResource(raw)
	require.NoError(t, err)
	target := &types.WindowTarget{Platform: "web", FormFactor: "desktop", Surface: "browser", Capabilities: []string{"markdown", "chart", "upload", "code", "diff"}}
	selected, err := compiled.Select(target)
	require.NoError(t, err)
	variantRaw, err := json.Marshal(selected)
	require.NoError(t, err)
	// Exercise isolation on the actual nested advertiser definition, before timing.
	selected.Window.View.Content.ID = "caller-mutated"
	for name := range selected.Window.DataSource {
		delete(selected.Window.DataSource, name)
	}
	selected, err = compiled.Select(target)
	require.NoError(t, err)
	detachedRaw, err := json.Marshal(selected)
	require.NoError(t, err)
	require.JSONEq(t, string(variantRaw), string(detachedRaw), "caller mutation cannot alter captured advertiser metadata")
	windowRaw, err := json.Marshal(selected.Window)
	require.NoError(t, err)
	var tree map[string]any
	require.NoError(t, json.Unmarshal(windowRaw, &tree))
	type presentationParts struct {
		View    types.View     `json:"view"`
		Dialogs []types.Dialog `json:"dialogs,omitempty"`
	}
	parts := presentationParts{View: selected.Window.View, Dialogs: selected.Window.Dialogs}
	partsRaw, err := json.Marshal(parts)
	require.NoError(t, err)
	var preparedParts map[string]any
	require.NoError(t, json.Unmarshal(partsRaw, &preparedParts)) // Match current projection's float64 semantics.
	var detachedParts map[string]any
	require.NoError(t, deepcopy.Copy(&detachedParts, &preparedParts, deepcopy.CopyViaCopyingMethod(false)))
	detachedParts["caller-only"] = true
	require.NotContains(t, preparedParts, "caller-only")
	delete(detachedParts, "caller-only")
	normalizedPartsRaw, err := json.Marshal(preparedParts)
	require.NoError(t, err)
	detachedPartsRaw, err := json.Marshal(detachedParts)
	require.NoError(t, err)
	require.Equal(t, normalizedPartsRaw, detachedPartsRaw, "prepared clone preserves current projection numeric/render semantics")
	decodeParts := func(value map[string]any) {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		var projected presentationParts
		require.NoError(t, json.Unmarshal(raw, &projected))
	}
	actor, verify := PublicReadActors()
	bindings, err := snapshot.Bindings("profile-owner", func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return authoredTestPolicy{a}, nil
	})
	require.NoError(t, err)
	provider, err := NewLocalProvider(LocalConfig{ProviderIdentity: "profile-owner", Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: bindings, Validators: map[string]LocalResourceValidator{"window": ValidateWindowBundle}})
	require.NoError(t, err)
	_ = provider // Capture validates actual owning-source preparation; measured phases are detached metadata work only.
	facts := profilePresentationFacts{snapshot: &permittedview.Snapshot{AuthorizationVersion: "fixed-ui-facts", ExpiresAt: time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC), Principal: map[string]any{"roles": []string{"editor"}, "features": []string{"history"}}, Resources: map[string]*permittedview.Resource{"42": {ID: 42, Type: "advertiser", Capabilities: map[string]bool{"read": false, "write": true, "history": true}}}}}
	projection := permittedview.NewRuntime(facts)
	presentation := &permittedview.BoundView{Window: selected.Window, ResourceID: 42, ResourceIDString: "42"}
	phases := map[string]func(){
		"compiled_select": func() { _, err := compiled.Select(target); require.NoError(t, err) },
		"variant_marshal": func() { _, err := json.Marshal(selected); require.NoError(t, err) },
		"variant_decode": func() {
			var v types.WindowResourceVariant
			decoder := json.NewDecoder(bytes.NewReader(variantRaw))
			decoder.UseNumber()
			require.NoError(t, decoder.Decode(&v))
		},
		"window_marshal":            func() { _, err := json.Marshal(selected.Window); require.NoError(t, err) },
		"window_tree_decode":        func() { var value map[string]any; require.NoError(t, json.Unmarshal(windowRaw, &value)) },
		"window_tree_encode":        func() { _, err := json.Marshal(tree); require.NoError(t, err) },
		"window_typed_decode":       func() { var w types.Window; require.NoError(t, json.Unmarshal(windowRaw, &w)) },
		"ui_projection_no_network":  func() { _, err := permittedview.NewRuntime(nil).Project(ctx, selected.Window); require.NoError(t, err) },
		"ui_projection_fixed_facts": func() { _, err := projection.Project(ctx, selected.Window, presentation); require.NoError(t, err) },
		"presentation_roundtrip": func() {
			raw, err := json.Marshal(parts)
			require.NoError(t, err)
			var value map[string]any
			require.NoError(t, json.Unmarshal(raw, &value))
			decodeParts(value)
		},
		"prepared_presentation_roundtrip": func() {
			var detached map[string]any
			require.NoError(t, deepcopy.Copy(&detached, &preparedParts, deepcopy.CopyViaCopyingMethod(false)))
			decodeParts(detached)
		},
	}
	t.Logf("PREPARATION payloadBytes=%d selectedVariantBytes=%d windowBytes=%d presentationBytes=%d sourceCompiles=%d", len(raw), len(variantRaw), len(windowRaw), len(partsRaw), snapshot.CompileCount())
	for _, name := range []string{"compiled_select", "variant_marshal", "variant_decode", "window_marshal", "window_tree_decode", "window_tree_encode", "window_typed_decode", "ui_projection_no_network", "ui_projection_fixed_facts", "presentation_roundtrip", "prepared_presentation_roundtrip"} {
		for repeat := 0; repeat < 3; repeat++ {
			phases[name]()
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			started := time.Now()
			for i := 0; i < 10; i++ {
				phases[name]()
			}
			elapsed := time.Since(started)
			runtime.ReadMemStats(&after)
			t.Logf("PREPARATION phase=%s repeat=%d iterations=10 avgMS=%.3f allocBytesPerOp=%d allocsPerOp=%d", name, repeat, float64(elapsed)/float64(time.Millisecond)/10, (after.TotalAlloc-before.TotalAlloc)/10, (after.Mallocs-before.Mallocs)/10)
		}
	}
}
