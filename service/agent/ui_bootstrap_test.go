package agent

import (
	"testing"
	"time"

	uireg "github.com/viant/agently-core/service/ui/window/registry"
)

func TestSelectWorkspaceUIBootstrapClientPrefersRequestClient(t *testing.T) {
	items := []uireg.ClientSnapshot{
		{
			ClientID:  "web-client",
			Snapshot:  &uireg.Snapshot{ClientID: "web-client"},
			UpdatedAt: time.Now(),
		},
		{
			ClientID:  "mobile-client",
			Snapshot:  &uireg.Snapshot{ClientID: "mobile-client"},
			UpdatedAt: time.Now().Add(-time.Second),
		},
	}

	got := selectWorkspaceUIBootstrapClient(items, " mobile-client ")

	if got == nil || got.ClientID != "mobile-client" {
		t.Fatalf("expected preferred mobile client, got %#v", got)
	}
}

func TestSelectWorkspaceUIBootstrapClientDoesNotFallbackWhenPreferredMissing(t *testing.T) {
	items := []uireg.ClientSnapshot{
		{
			ClientID:  "web-client",
			Snapshot:  &uireg.Snapshot{ClientID: "web-client"},
			UpdatedAt: time.Now(),
		},
	}

	if got := selectWorkspaceUIBootstrapClient(items, "mobile-client"); got != nil {
		t.Fatalf("expected no snapshot for missing preferred client, got %#v", got)
	}
}

func TestSelectWorkspaceUIBootstrapClientUsesFirstAvailableWithoutPreferredClient(t *testing.T) {
	items := []uireg.ClientSnapshot{
		{
			ClientID:  "web-client",
			Snapshot:  &uireg.Snapshot{ClientID: "web-client"},
			UpdatedAt: time.Now(),
		},
		{
			ClientID:  "mobile-client",
			Snapshot:  &uireg.Snapshot{ClientID: "mobile-client"},
			UpdatedAt: time.Now().Add(-time.Second),
		},
	}

	got := selectWorkspaceUIBootstrapClient(items, "")

	if got == nil || got.ClientID != "web-client" {
		t.Fatalf("expected first available client without preference, got %#v", got)
	}
}
