package workspace

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLayoutSectionPresentation(t *testing.T) {
	for _, presentation := range []string{"", "sections", "unknown"} {
		var layout Layout
		input := "version: 1\nid: example\nleft:\n  navigation:\n    presentation: " + presentation + "\napplications: []\n"
		if err := yaml.Unmarshal([]byte(input), &layout); err != nil {
			t.Fatal(err)
		}
		err := validateLayout(&layout)
		if presentation == "unknown" {
			if err == nil || !strings.Contains(err.Error(), "navigation presentation") {
				t.Fatalf("expected unsupported presentation, got %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(layout)
		if err != nil {
			t.Fatal(err)
		}
		if presentation == "sections" && !strings.Contains(string(data), `"presentation":"sections"`) {
			t.Fatalf("presentation lost in host delivery: %s", data)
		}
		if presentation == "" && strings.Contains(string(data), `"presentation"`) {
			t.Fatalf("legacy layout changed: %s", data)
		}
	}
}
