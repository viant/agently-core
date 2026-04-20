package agent

import "testing"

func TestResolveUserAsk(t *testing.T) {
	if got := resolveUserAsk(&QueryInput{
		Context: map[string]interface{}{
			"intake.title": "Distilled ask",
		},
	}, "Raw display query"); got != "Distilled ask" {
		t.Fatalf("resolveUserAsk() with intake title = %q", got)
	}

	if got := resolveUserAsk(&QueryInput{}, "Raw display query"); got != "Raw display query" {
		t.Fatalf("resolveUserAsk() fallback = %q", got)
	}
}
