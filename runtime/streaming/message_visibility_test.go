package streaming

import "testing"

func TestInternalMessageModeUsesMetadataOnly(t *testing.T) {
	for _, mode := range []string{"router", "chain", " Router ", "CHAIN"} {
		if !IsInternalMessageMode(mode) {
			t.Fatalf("internal mode visible: %q", mode)
		}
	}
	for _, mode := range []string{"", "task", "narrator", "summary", "plan", "unknown"} {
		if IsInternalMessageMode(mode) {
			t.Fatalf("visible mode hidden: %q", mode)
		}
	}
}
