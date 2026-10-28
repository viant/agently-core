package executor

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	resources "github.com/viant/agently-core/service/resource"
)

type lockedMCPDefinitionLogs struct {
	mu sync.Mutex
	bytes.Buffer
}

func (l *lockedMCPDefinitionLogs) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Buffer.Write(data)
}

func (l *lockedMCPDefinitionLogs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Buffer.String()
}

func captureMCPDefinitionLogs(t *testing.T) *lockedMCPDefinitionLogs {
	t.Helper()
	output := &lockedMCPDefinitionLogs{}
	previous := log.Writer()
	log.SetOutput(output)
	t.Cleanup(func() { log.SetOutput(previous) })
	return output
}

func testMCPDefinitionFailure(operationID, requestID string) resources.MCPReferenceFailure {
	return resources.MCPReferenceFailure{
		Operation: "list", Connection: "example", Category: "denied", RPCCode: -32003,
		RPCStatus: 403, OperationID: operationID, Stage: "remote_response", Origin: "remote_http",
		Reason: "client_initialization_failed", CredentialType: "unknown", CredentialPresent: "unknown",
		HTTPStatus: 403, RemoteRequestID: requestID,
	}
}

func TestMCPDefinitionFailureAggregatorSummarizesWithinWindowAndFlushesOnClose(t *testing.T) {
	t.Setenv("AGENTLY_DEBUG", "off")
	output := captureMCPDefinitionLogs(t)
	aggregator := newMCPDefinitionFailureAggregator(time.Hour)
	aggregator.Record(context.Background(), testMCPDefinitionFailure("operation-1", "request-1"))
	require.Equal(t, 1, strings.Count(output.String(), "mcp.definition.failed "))
	require.NotContains(t, output.String(), "mcp.definition.failed.summary")
	aggregator.Record(context.Background(), testMCPDefinitionFailure("operation-2", "request-2"))
	require.Equal(t, 1, strings.Count(output.String(), "mcp.definition.failed "))
	aggregator.Close()
	logs := output.String()
	require.Contains(t, logs, `count=2 window="1h0m0s" requestPairs=[{"operationID":"operation-1","remoteRequestID":"request-1"},{"operationID":"operation-2","remoteRequestID":"request-2"}]`)
	require.Equal(t, 1, strings.Count(logs, "mcp.definition.failed.summary"))
	aggregator.Close()
	require.Equal(t, logs, output.String())
	aggregator.Record(context.Background(), testMCPDefinitionFailure("after-close", "request-after-close"))
	require.Contains(t, output.String(), `operationID="after-close"`)
}

func TestMCPDefinitionFailureAggregatorFlushesAtLimit(t *testing.T) {
	output := captureMCPDefinitionLogs(t)
	aggregator := newMCPDefinitionFailureAggregator(time.Hour)
	for i := range mcpDefinitionFailureGroupSize {
		aggregator.Record(context.Background(), testMCPDefinitionFailure(fmt.Sprintf("operation-%d", i), fmt.Sprintf("request-%d", i)))
	}
	logs := output.String()
	require.Equal(t, 1, strings.Count(logs, "mcp.definition.failed "))
	require.Contains(t, logs, "count=32")
	require.Contains(t, logs, `{"operationID":"operation-31","remoteRequestID":"request-31"}`)
	aggregator.Record(context.Background(), testMCPDefinitionFailure("operation-new", "request-new"))
	require.Equal(t, 2, strings.Count(output.String(), "mcp.definition.failed "))
	aggregator.Close()
}

func TestMCPDefinitionFailureAggregatorExpiresAndSeparatesCauses(t *testing.T) {
	output := captureMCPDefinitionLogs(t)
	aggregator := newMCPDefinitionFailureAggregator(20 * time.Millisecond)
	aggregator.Record(context.Background(), testMCPDefinitionFailure("operation-1", "request-1"))
	aggregator.Record(context.Background(), testMCPDefinitionFailure("operation-2", "request-2"))
	other := testMCPDefinitionFailure("operation-3", "request-3")
	other.Connection = "other"
	aggregator.Record(context.Background(), other)
	require.Eventually(t, func() bool { return strings.Contains(output.String(), "mcp.definition.failed.summary") }, time.Second, time.Millisecond)
	aggregator.Close()
	require.Equal(t, 1, strings.Count(output.String(), "mcp.definition.failed.summary"))
	require.Equal(t, 2, strings.Count(output.String(), "mcp.definition.failed "))
}

func TestMCPDefinitionFailureAggregatorEvictsOldestGroup(t *testing.T) {
	output := captureMCPDefinitionLogs(t)
	aggregator := newMCPDefinitionFailureAggregator(time.Hour)
	aggregator.Record(context.Background(), testMCPDefinitionFailure("first-1", "remote-1"))
	aggregator.Record(context.Background(), testMCPDefinitionFailure("first-2", "remote-2"))
	for i := 1; i <= mcpDefinitionFailureGroupMax; i++ {
		failure := testMCPDefinitionFailure(fmt.Sprintf("operation-%d", i), "")
		failure.Connection = fmt.Sprintf("connection-%d", i)
		aggregator.Record(context.Background(), failure)
	}
	require.Equal(t, mcpDefinitionFailureGroupMax, len(aggregator.groups))
	require.Contains(t, output.String(), `count=2 window="1h0m0s" requestPairs=[{"operationID":"first-1","remoteRequestID":"remote-1"},{"operationID":"first-2","remoteRequestID":"remote-2"}]`)
	aggregator.Close()
}
