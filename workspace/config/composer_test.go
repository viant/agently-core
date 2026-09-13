package config

import "testing"

func TestComposerAvailability(t *testing.T) {
	var missing *Root
	if value := missing.Composer(); !value.AllowAgentSelection || !value.AllowModelSelection {
		t.Fatal(value)
	}
	root := &Root{Raw: map[string]interface{}{"ui": map[string]interface{}{"composer": map[string]interface{}{"allowAgentSelection": false, "allowModelSelection": false}}}}
	if value := root.Composer(); value.AllowAgentSelection || value.AllowModelSelection {
		t.Fatal(value)
	}
}
