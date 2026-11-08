package view

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
)

func TestDiscoveryTargetRetainsPreferencesWithoutAdmissionProof(t *testing.T) {
	private := &types.WindowTarget{Platform: "web", FormFactor: "phone", Surface: "dialog", Capabilities: []string{"charts"}, SelectionToken: "server-proof", DependencyPins: map[string]identity.ResolvedResource{"rows": {URI: "datasource://example/rows"}}}
	public := discoveryWindowTarget(private)
	require.Equal(t, private.Platform, public.Platform)
	require.Equal(t, private.FormFactor, public.FormFactor)
	require.Equal(t, private.Surface, public.Surface)
	require.Equal(t, private.Capabilities, public.Capabilities)
	require.Empty(t, public.SelectionToken)
	require.Empty(t, public.DependencyPins)
	require.Equal(t, "server-proof", private.SelectionToken)
	require.Nil(t, discoveryWindowTarget(&types.WindowTarget{SelectionToken: "server-proof"}))
}

func TestGetDiscoveryTargetOpensThroughFreshNativeAdmission(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		views, bridge, policy, _, ctx := canonicalLifecycleFixture(t, root)
		policy.update("reader/account/one", "1", true)
		listed := &ListOutput{}
		require.NoError(t, views.list(ctx, &ListInput{}, listed))
		require.Len(t, listed.Items, 1)
		require.Nil(t, listed.Items[0].Target, "legacy catalog discovery must not leak a proof")
		listedDone := serveCanonicalOpen(t, bridge)
		require.NoError(t, views.open(ctx, &OpenInput{ID: listed.Items[0].ID, Target: listed.Items[0].Target, ClientID: "client", TimeoutMs: 2000}, &OpenOutput{}))
		require.NoError(t, <-listedDone)
		got := &GetOutput{}
		require.NoError(t, views.get(ctx, &GetInput{ID: "overview"}, got))
		// Serialize exactly what a consumer can copy into the public Open contract.
		raw, err := json.Marshal(got.Item)
		require.NoError(t, err)
		var copied ListItem
		require.NoError(t, json.Unmarshal(raw, &copied))
		require.Nil(t, copied.Target, "a server signature is not an open preference")
		done := serveCanonicalOpen(t, bridge)
		output := &OpenOutput{}
		require.NoError(t, views.open(ctx, &OpenInput{ID: copied.ID, Target: copied.Target, ClientID: "client", TimeoutMs: 2000}, output))
		require.NoError(t, <-done)
		require.NotEmpty(t, output.WindowID)
		policy.update("reader/account/one", "1", false)
		require.Error(t, views.get(context.Background(), &GetInput{ID: "overview"}, &GetOutput{}), "discovery does not bypass producer denial")
	})
}

func TestGetDiscoveryPreferencesSelectSameNativeVariantOnOpen(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		views, _, policy, source, ctx := canonicalLifecycleFixture(t, root)
		policy.update("reader/account/one", "1", true)
		preference := types.WindowTarget{Platform: "web", FormFactor: "phone", Surface: "dialog"}
		variant := types.WindowResourceVariant{Window: &types.Window{View: types.View{Content: &types.Container{ID: "phone-content"}}}, DataSources: map[string]json.RawMessage{}}
		hash, err := types.WindowVariantFingerprint(variant)
		require.NoError(t, err)
		envelope := types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Targets: []types.WindowTargetBinding{{Variant: hash}, {Target: preference, Variant: hash}}, Variants: map[string]types.WindowResourceVariant{hash: variant}}
		require.NoError(t, envelope.Validate())
		source.raw["1"], err = json.Marshal(envelope)
		require.NoError(t, err)
		got := &GetOutput{}
		require.NoError(t, views.get(ctx, &GetInput{ID: "overview", Target: &preference}, got))
		require.Equal(t, &preference, got.Item.Target)
		prepared, err := views.prepareOpenItem(ctx, OpenItem{ID: got.Item.ID, Target: got.Item.Target})
		require.NoError(t, err)
		require.Equal(t, preference.ProfileKey(), prepared.item.Target.ProfileKey())
		require.NotEmpty(t, prepared.item.Target.SelectionToken, "only fresh native admission returns the internal proof")
		unsupported := preference
		unsupported.FormFactor = "unsupported"
		_, err = views.prepareOpenItem(ctx, OpenItem{ID: got.Item.ID, Target: &unsupported})
		require.Error(t, err)
		forged := preference
		forged.SelectionToken = "untrusted-proof"
		_, err = views.prepareOpenItem(ctx, OpenItem{ID: got.Item.ID, Target: &forged})
		require.Error(t, err, "explicit counterfeit admission remains denied")
	})
}
