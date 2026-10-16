package workspace

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLayoutMenuCanonicalWindowIdentity(t *testing.T) {
	for _, key := range []string{"overview", "Window-A", "window://example/StudioRecords", "window://example/Studio/Records"} {
		t.Run(key, func(t *testing.T) {
			var layout Layout
			require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf("version: 1\nid: main\napplications:\n  - id: steward\n    title: Steward\n    menus:\n      - id: owned-studio-records\n        title: Studio Records\n        action: {type: window, windowKey: %q}\n", key)), &layout))
			require.NoError(t, validateLayout(&layout))
			require.Equal(t, key, layout.Applications[0].Menus[0].Action.WindowKey)
		})
	}
	for _, key := range []string{
		"window:///Records", "window://example/", "window://example//Records",
		"window://example/../Records", "window://example/Studio/./Records",
		"window://example/Studio%2fRecords", "window://example/Records?revision=1",
		"window://example/Records#view", "window://bad namespace/Records",
		"report://example/Records", "https://example/Records", "Window://example/Records",
		"window://example/Records://other", "Studio/Records", "../Records", "Records?x=1", "provider:studio:Records",
	} {
		t.Run(key, func(t *testing.T) {
			layout := &Layout{Version: 1, ID: "main", Applications: []LayoutApplication{{ID: "steward", Title: "Steward", Menus: []LayoutMenu{{ID: "records", Title: "Records", Action: &LayoutAction{Type: "window", WindowKey: key}}}}}}
			require.ErrorContains(t, validateLayout(layout), "invalid window key")
		})
	}
}
