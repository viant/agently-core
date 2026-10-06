package agui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/agui/extensions"
)

func TestFeedLifecycleFactValidationAndOriginalSourceAuthority(t *testing.T) {
	at := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	read := func(content map[string]any, metadata any) FeedLifecycleFact {
		raw, _ := json.Marshal(map[string]any{"type": "ACTIVITY_SNAPSHOT", "activityType": "agently.feed", "content": content, "metadata": metadata})
		fact, valid := ReadFeedLifecycleFact(raw, "thread", "feed")
		require.True(t, valid)
		return fact
	}
	original := read(map[string]any{"version": "1", "feedId": "feed", "active": false, "activationAt": at.Format(time.RFC3339Nano)}, nil)
	original.Journal = &FeedLifecycleOrigin{RunID: "original", Sequence: 2}
	// A copy has no authority to relabel itself as a new transition.
	inherited := read(map[string]any{"version": "1", "feed": map[string]any{"feedId": "feed"}, "active": false, "activationKnown": true, "activationAt": at.Format(time.RFC3339Nano), "activationSource": map[string]any{"runId": "copy", "sequence": 7}}, nil)
	inherited.Journal = &FeedLifecycleOrigin{RunID: "copy", Sequence: 7}
	foreign := inherited
	foreign.Origin = &FeedLifecycleOrigin{RunID: "foreign", Sequence: 9}
	matched := inherited
	matched.Origin = &FeedLifecycleOrigin{RunID: "original", Sequence: 2}
	changed := matched
	changed.Active = true
	facts := ValidateFeedLifecycleSources([]FeedLifecycleFact{original, inherited, foreign, matched, changed})
	require.Equal(t, original.Journal, facts[0].Origin)
	require.Nil(t, facts[1].Origin)
	require.Nil(t, facts[2].Origin)
	require.Equal(t, original.Journal, facts[3].Origin)
	require.Nil(t, facts[4].Origin)
	// Refresh emission timestamps never date an inherited legacy fact.
	undated := read(map[string]any{"version": "1", "feed": map[string]any{"feedId": "feed"}, "active": false, "activationKnown": true}, map[string]any{"agently": map[string]any{"presentation": map[string]any{"createdAt": at.Format(time.RFC3339Nano)}}})
	require.True(t, undated.At.IsZero())
	native := read(map[string]any{"version": "1", "feedId": "feed", "active": false}, map[string]any{"agently": map[string]any{"presentation": map[string]any{"createdAt": at.Format(time.RFC3339Nano)}}})
	require.True(t, native.At.Equal(at))
}
func TestFeedSnapshotUnknownCanRetainAnExplicitOriginalFact(t *testing.T) {
	content := map[string]any{"version": "1", "feed": map[string]any{"feedId": "feed"}, "active": nil, "activationKnown": false, "activationFact": map[string]any{"version": "1", "feedId": "feed", "active": true, "activationSource": map[string]any{"runId": "run", "sequence": 3}}}
	raw, _ := json.Marshal(content)
	require.NoError(t, extensions.ValidatePresentation("FeedActivationSnapshot", raw))
	event, _ := json.Marshal(map[string]any{"type": "ACTIVITY_SNAPSHOT", "activityType": "agently.feed", "content": content})
	fact, valid := ReadFeedLifecycleFact(event, "thread", "feed")
	require.True(t, valid)
	require.True(t, fact.Active)
	require.True(t, fact.At.IsZero())
	require.Equal(t, &FeedLifecycleOrigin{RunID: "run", Sequence: 3}, fact.Origin)
	for _, invalid := range []map[string]any{{"runId": "", "sequence": 3}, {"runId": "run", "sequence": 0}, {"runId": "run", "sequence": 1.5}, {"runId": "run", "sequence": 3, "threadId": "foreign"}} {
		nested := content["activationFact"].(map[string]any)
		nested["activationSource"] = invalid
		bad, _ := json.Marshal(content)
		require.Error(t, extensions.ValidatePresentation("FeedActivationSnapshot", bad))
		event, _ = json.Marshal(map[string]any{"type": "ACTIVITY_SNAPSHOT", "activityType": "agently.feed", "content": content})
		_, valid = ReadFeedLifecycleFact(event, "thread", "feed")
		require.False(t, valid)
	}
}

func TestFeedFactsRequireRequestedFeedAndScopedEventIdentity(t *testing.T) {
	base := map[string]any{"type": "ACTIVITY_SNAPSHOT", "activityType": "agently.feed", "threadId": "thread", "runId": "run", "content": map[string]any{"version": "1", "feedId": "feed", "active": false}}
	for _, test := range []struct {
		name  string
		patch func(map[string]any)
	}{
		{"thread", func(e map[string]any) { e["threadId"] = "foreign" }},
		{"run", func(e map[string]any) { e["runId"] = "foreign" }},
		{"feed", func(e map[string]any) { e["content"].(map[string]any)["feedId"] = "foreign" }},
		{"version", func(e map[string]any) { e["content"].(map[string]any)["version"] = "2" }},
		{"non-bool", func(e map[string]any) { e["content"].(map[string]any)["active"] = "false" }},
		{"unknown", func(e map[string]any) { e["content"].(map[string]any)["active"] = nil }},
		{"activity-type", func(e map[string]any) { e["activityType"] = "foreign.feed" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			blob, _ := json.Marshal(base)
			var event map[string]any
			require.NoError(t, json.Unmarshal(blob, &event))
			test.patch(event)
			blob, _ = json.Marshal(event)
			_, valid := ReadFeedLifecycleFact(blob, "thread", "feed", "run")
			require.False(t, valid)
		})
	}
}
