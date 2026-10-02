package native

import (
	"context"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestProvisionedSQLiteRetainsOriginalDriverAndPragmas(t *testing.T) {
	for _, driver := range []string{"", "sqlite", "sqlite3"} {
		t.Run("driver="+driver, func(t *testing.T) {
			for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
				t.Setenv(key, "")
			}
			t.Setenv("AGENTLY_DB_DRIVER", driver)
			configured, err := connectorFromEnvironment(context.Background(), t.TempDir())
			require.NoError(t, err)
			require.Equal(t, 2, configured.MaxOpenConns)
			require.Equal(t, 2, configured.MaxIdleConns)
			require.Contains(t, configured.DSN, "cache=shared")
			if driver == "sqlite3" {
				require.Equal(t, "sqlite3", configured.Driver)
				require.Contains(t, configured.DSN, "_busy_timeout=5000")
				require.NotContains(t, configured.DSN, "_pragma")
			} else {
				require.Equal(t, "sqlite", configured.Driver)
				require.Contains(t, configured.DSN, "_pragma=busy_timeout(5000)")
				require.False(t, strings.Contains(configured.DSN, "_busy_timeout="))
			}
		})
	}
}
func TestConfiguredSQLiteDSNAndDriverRemainExplicit(t *testing.T) {
	for _, driver := range []string{"sqlite", "sqlite3"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("AGENTLY_DB_DRIVER", driver)
			t.Setenv("AGENTLY_DB_SECRETS", "")
			dsn := "file:" + t.TempDir() + "/explicit.db?cache=private"
			t.Setenv("AGENTLY_DB_DSN", dsn)
			configured, err := connectorFromEnvironment(context.Background(), "")
			require.NoError(t, err)
			require.Equal(t, driver, configured.Driver)
			require.Equal(t, dsn, configured.DSN)
		})
	}
}
