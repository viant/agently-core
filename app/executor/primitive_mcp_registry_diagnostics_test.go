package executor

import (
	"bytes"
	"context"
	"log"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	resources "github.com/viant/agently-core/service/resource"
)

func TestMCPDefinitionFailureLogIsOperationalAndExcludesSecrets(t *testing.T) {
	t.Setenv("AGENTLY_DEBUG", "off")
	t.Setenv("AGENTLY_DEBUG_COMPONENTS", "other")
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	aggregator := newMCPDefinitionFailureAggregator(time.Hour)
	defer aggregator.Close()
	aggregator.Record(context.Background(), resources.MCPReferenceFailure{
		Operation: "list", Connection: "example", Category: "denied", RPCCode: -32003, RPCStatus: 403,
		OperationID: "1234567890abcdef", Stage: "remote_response", Origin: "remote_rpc", Reason: "resource_list_failed",
		CredentialType: "id_token", CredentialPresent: "false", HTTPStatus: 200, RemoteRequestID: "request-123",
	})
	logs := output.String()
	require.Contains(t, logs, `mcp.definition.failed operation="list" connection="example"`)
	require.Contains(t, logs, `rpcStatus=403 operationID="1234567890abcdef" stage="remote_response" origin="remote_rpc"`)
	require.Contains(t, logs, `credentialType="id_token" credentialPresent="false" httpStatus=200 remoteRequestID="request-123"`)
	require.NotContains(t, logs, "private-token")
}
