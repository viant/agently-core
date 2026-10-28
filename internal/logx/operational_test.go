package logx

import (
	"bytes"
	"log"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOperationalWarningIgnoresDebugFiltersAndRedactsResourceCredentials(t *testing.T) {
	t.Setenv("AGENTLY_DEBUG", "off")
	t.Setenv("AGENTLY_DEBUG_COMPONENTS", "other")
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	OperationalWarnf("resource", "read.failed resource=%q", SafeResourceIdentifier("mcp://user:secret@example.test/catalog?token=secret"))
	require.Contains(t, output.String(), `[warn][resource] read.failed resource="<redacted>"`)
	require.NotContains(t, output.String(), "secret")
	require.Equal(t, "mcp://workspace/example", SafeResourceIdentifier("mcp://workspace/example"))
}
