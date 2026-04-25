package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig_ExpandsOAuthConfigURLTemplate(t *testing.T) {
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

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil || cfg.OAuth == nil || cfg.OAuth.Client == nil {
		t.Fatalf("expected oauth client config to be loaded")
	}
	if got, want := cfg.OAuth.Client.ConfigURL, "idp_override.enc|blowfish://default"; got != want {
		t.Fatalf("ConfigURL = %q, want %q", got, want)
	}
}

func TestLoadConfig_UsesOAuthConfigURLDefaultWhenEnvUnset(t *testing.T) {
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

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil || cfg.OAuth == nil || cfg.OAuth.Client == nil {
		t.Fatalf("expected oauth client config to be loaded")
	}
	if got, want := cfg.OAuth.Client.ConfigURL, "idp_viant.enc|blowfish://default"; got != want {
		t.Fatalf("ConfigURL = %q, want %q", got, want)
	}
}

func TestDetectDBDialect(t *testing.T) {
	testCases := []struct {
		name    string
		driver  interface{}
		want    string
		wantErr string
	}{
		{name: "mysql", driver: &mysqlDriver{}, want: "mysql"},
		{name: "sqlite", driver: &sqliteDriver{}, want: "sqlite"},
		{name: "nil", driver: nil, wantErr: "nil db driver"},
		{name: "unsupported", driver: struct{}{}, wantErr: "unsupported db driver"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := detectDBDialect(testCase.driver)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("expected error containing %q, got %v", testCase.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("expected dialect %q, got %q", testCase.want, got)
			}
		})
	}
}

type mysqlDriver struct{}

type sqliteDriver struct{}

func TestRefreshLeaseSQL(t *testing.T) {
	mysqlSQL, err := refreshLeaseSQL("mysql")
	if err != nil {
		t.Fatalf("unexpected mysql error: %v", err)
	}
	if !strings.Contains(mysqlSQL, "UTC_TIMESTAMP() + INTERVAL ? SECOND") {
		t.Fatalf("expected mysql query to use UTC_TIMESTAMP interval, got %q", mysqlSQL)
	}
	if !strings.Contains(mysqlSQL, "lease_until IS NULL OR lease_until < UTC_TIMESTAMP()") {
		t.Fatalf("expected mysql query to include NULL/expired lease check, got %q", mysqlSQL)
	}

	sqliteSQL, err := refreshLeaseSQL("sqlite")
	if err != nil {
		t.Fatalf("unexpected sqlite error: %v", err)
	}
	if !strings.Contains(sqliteSQL, "DATETIME('now', '+' || ? || ' seconds')") {
		t.Fatalf("expected sqlite query to use DATETIME now arithmetic, got %q", sqliteSQL)
	}
	if !strings.Contains(sqliteSQL, "lease_until IS NULL OR lease_until < DATETIME('now')") {
		t.Fatalf("expected sqlite query to include NULL/expired lease check, got %q", sqliteSQL)
	}

	if _, err := refreshLeaseSQL("postgres"); err == nil {
		t.Fatalf("expected unsupported dialect error")
	}
}

func TestCASPutSQL(t *testing.T) {
	mysqlSQL, err := casPutSQL("mysql")
	if err != nil {
		t.Fatalf("unexpected mysql error: %v", err)
	}
	if !strings.Contains(mysqlSQL, "updated_at = UTC_TIMESTAMP()") {
		t.Fatalf("expected mysql query to use UTC_TIMESTAMP, got %q", mysqlSQL)
	}

	sqliteSQL, err := casPutSQL("sqlite")
	if err != nil {
		t.Fatalf("unexpected sqlite error: %v", err)
	}
	if !strings.Contains(sqliteSQL, "updated_at = DATETIME('now')") {
		t.Fatalf("expected sqlite query to use DATETIME('now'), got %q", sqliteSQL)
	}

	if _, err := casPutSQL("postgres"); err == nil {
		t.Fatalf("expected unsupported dialect error")
	}
}
