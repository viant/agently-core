package executor

import (
	"testing"

	"github.com/viant/agently-core/app/executor/config"
)

func TestResolveReportingStoreDefaults_UsesConfiguredValues(t *testing.T) {
	defaults := &config.Defaults{}
	defaults.Reporting.Enabled = true
	defaults.Reporting.Store.Backend = "sql"
	defaults.Reporting.Store.ConnectorRef = "custom"

	store := resolveReportingStoreDefaults(defaults)
	if store.Backend != "sql" {
		t.Fatalf("expected sql backend, got %q", store.Backend)
	}
	if store.ConnectorRef != "custom" {
		t.Fatalf("expected connector ref custom, got %q", store.ConnectorRef)
	}
}

func TestResolveReportingStoreDefaults_UsesAgentlyDBEnvDefaults(t *testing.T) {
	t.Setenv("AGENTLY_DB_DSN", "sqlite:///tmp/agently.db")
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")

	defaults := &config.Defaults{}
	defaults.Reporting.Enabled = true

	store := resolveReportingStoreDefaults(defaults)
	if store.Backend != "sql" {
		t.Fatalf("expected sql backend, got %q", store.Backend)
	}
	if store.ConnectorRef != defaultReportingStoreConnectorRef {
		t.Fatalf("expected default connector ref %q, got %q", defaultReportingStoreConnectorRef, store.ConnectorRef)
	}
}

func TestResolveReportingStoreDefaults_LeavesBackendBlankWithoutDBEnv(t *testing.T) {
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")

	defaults := &config.Defaults{}
	defaults.Reporting.Enabled = true

	store := resolveReportingStoreDefaults(defaults)
	if store.Backend != "" {
		t.Fatalf("expected blank backend, got %q", store.Backend)
	}
	if store.ConnectorRef != "" {
		t.Fatalf("expected blank connector ref, got %q", store.ConnectorRef)
	}
}

func TestReportingSQLStoreEnabled_UsesResolvedDefaults(t *testing.T) {
	t.Setenv("AGENTLY_DB_DSN", "sqlite:///tmp/agently.db")
	defaults := &config.Defaults{}
	defaults.Reporting.Enabled = true

	if !reportingSQLStoreEnabled(defaults) {
		t.Fatalf("expected sql reporting store to auto-enable when AGENTLY_DB_DSN is set")
	}
}
