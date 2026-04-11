package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWorkspaceConfig_ExpandsOAuthConfigURLTemplate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEWARD_OAUTH_CONFIG_URL", "idp_override.enc|blowfish://default")

	config := `auth:
  enabled: true
  cookieName: agently_session
  ipHashKey: dev-hmac-salt
  oauth:
    mode: bff
    client:
      configURL: ${STEWARD_OAUTH_CONFIG_URL:-idp_viant.enc|blowfish://default}
`
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatalf("failed to write config.yaml: %v", err)
	}

	cfg, err := LoadWorkspaceConfig(root)
	if err != nil {
		t.Fatalf("LoadWorkspaceConfig() error = %v", err)
	}
	if cfg == nil || cfg.OAuth == nil || cfg.OAuth.Client == nil {
		t.Fatalf("expected oauth client config to be loaded")
	}
	if got, want := cfg.OAuth.Client.ConfigURL, "idp_override.enc|blowfish://default"; got != want {
		t.Fatalf("ConfigURL = %q, want %q", got, want)
	}
}

func TestLoadWorkspaceConfig_UsesOAuthConfigURLDefaultWhenEnvUnset(t *testing.T) {
	root := t.TempDir()

	config := `auth:
  enabled: true
  cookieName: agently_session
  ipHashKey: dev-hmac-salt
  oauth:
    mode: bff
    client:
      configURL: ${STEWARD_OAUTH_CONFIG_URL:-idp_viant.enc|blowfish://default}
`
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatalf("failed to write config.yaml: %v", err)
	}

	cfg, err := LoadWorkspaceConfig(root)
	if err != nil {
		t.Fatalf("LoadWorkspaceConfig() error = %v", err)
	}
	if cfg == nil || cfg.OAuth == nil || cfg.OAuth.Client == nil {
		t.Fatalf("expected oauth client config to be loaded")
	}
	if got, want := cfg.OAuth.Client.ConfigURL, "idp_viant.enc|blowfish://default"; got != want {
		t.Fatalf("ConfigURL = %q, want %q", got, want)
	}
}
