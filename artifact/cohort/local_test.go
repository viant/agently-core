package cohort

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalCohortConfigMultipleProviders(t *testing.T) {
	entry := func(provider string) map[string]any {
		return map[string]any{"connectionName": provider, "providerIdentity": provider,
			"expectedEndpoint": "http://127.0.0.1:8096/mcp", "origin": "http://127.0.0.1:8095",
			"oauthClient": "/private/idp.enc", "userSecret": "/private/user.enc",
			"key": "blowfish://default", "expectedSubject": "verified-user"}
	}
	for _, tc := range []struct {
		name string
		make func() any
		want int
	}{
		{"legacy object", func() any { return entry("catalog") }, 1},
		{"subject pin optional", func() any { config := entry("catalog"); delete(config, "expectedSubject"); return config }, 1},
		{"two providers", func() any { return []any{entry("catalog"), entry("dspui")} }, 2},
		{"empty list", func() any { return []any{} }, 0},
		{"duplicate provider", func() any {
			a, b := entry("catalog"), entry("dspui")
			b["providerIdentity"] = a["providerIdentity"]
			return []any{a, b}
		}, 0},
		{"duplicate connection", func() any {
			a, b := entry("catalog"), entry("dspui")
			b["connectionName"] = a["connectionName"]
			return []any{a, b}
		}, 0},
		{"unknown second field", func() any {
			a, b := entry("catalog"), entry("dspui")
			b["browserRole"] = "admin"
			return []any{a, b}
		}, 0},
		{"missing second pin", func() any {
			a, b := entry("catalog"), entry("dspui")
			delete(b, "expectedEndpoint")
			return []any{a, b}
		}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.make())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "cohorts.json")
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			configs, err := loadArtifactCohortLocalConfigs(path)
			if tc.want == 0 {
				if err == nil {
					t.Fatal("invalid provider configuration accepted")
				}
				return
			}
			if err != nil || len(configs) != tc.want {
				t.Fatalf("providers=%d err=%v", len(configs), err)
			}
			for _, config := range configs {
				if len(config.Scopes) != 1 || config.Scopes[0] != "openid" {
					t.Fatal("default scope missing")
				}
			}
			if tc.want == 2 && configs[1].ProviderIdentity != "dspui" {
				t.Fatal("provider ordering changed")
			}
			if err = os.WriteFile(path, append(raw, []byte("\n---\nignored: true\n")...), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = loadArtifactCohortLocalConfigs(path); err == nil {
				t.Fatal("trailing configuration document accepted")
			}
		})
	}
}
