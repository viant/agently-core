package tool

import "testing"

func TestExplicitQualifiedNameKeepsLiteralMethodAndCatalogServer(t *testing.T) {
	for _, tc := range []struct{ name, server, method string }{{"independent:cursor-number", "independent", "cursor-number"}, {"independent:cursor.number-list", "independent", "cursor.number-list"}, {"inventory/planner:windows/list", "inventory/planner", "windows/list"}} {
		server, method := splitToolName(tc.name)
		if server != tc.server || method != tc.method {
			t.Fatalf("literal method misparsed: %s -> %s %s", tc.name, server, method)
		}
	}
	for _, raw := range []string{"windows/list", "cursor-number", "cursor.number-list"} {
		if qualifiedToolName("actual-server", raw) != "actual-server/"+raw || displayToolName("actual-server", raw) != "actual-server:"+raw {
			t.Fatal("remote method inferred another server", raw)
		}
	}
	server, method := splitToolName("legacy_server-method")
	if server != "legacy/server" || method != "method" {
		t.Fatal("legacy canonical fallback changed", server, method)
	}
}

func TestExplicitInternalLegacyQualifiedCatalogNameRemainsStable(t *testing.T) {
	if legacyInternalQualifiedToolName("analyst", "analyst:ResourceTree") != "analyst:ResourceTree" {
		t.Fatal("internal legacy qualification changed")
	}
	if qualifiedToolName("actual-server", "analyst:ResourceTree") != "actual-server/analyst:ResourceTree" {
		t.Fatal("external literal qualified-looking method inferred a server")
	}
}
