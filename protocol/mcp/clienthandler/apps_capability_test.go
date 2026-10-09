package clienthandler

import (
	"context"
	"github.com/stretchr/testify/require"
	mcpschema "github.com/viant/mcp-protocol/schema"
	"testing"
)

func TestInitAdvertisesOfficialAppsExtensionSeparatelyFromViewHandshake(t *testing.T) {
	caps := &mcpschema.ClientCapabilities{Extensions: map[string]map[string]interface{}{"vendor/feature": {"enabled": true}}}
	(&Handler{}).Init(context.Background(), caps)
	require.Equal(t, []string{"text/html;profile=mcp-app"}, caps.Extensions["io.modelcontextprotocol/ui"]["mimeTypes"])
	require.Nil(t, caps.Experimental["io.modelcontextprotocol/ui"])
	require.Nil(t, caps.Extensions["io.modelcontextprotocol/ui"]["protocolVersion"])
	require.Equal(t, true, caps.Extensions["vendor/feature"]["enabled"])
}
