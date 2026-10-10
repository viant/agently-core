package tool_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	native "github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	conversation "github.com/viant/agently-core/internal/service/conversation"
	registry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/mcp/clienthandler"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	resources "github.com/viant/agently-core/protocol/tool/service/resources"
	scratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk"
	"github.com/viant/agently-core/service/shared/toolexec"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp"
	protocolclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/logger"
	schema "github.com/viant/mcp-protocol/schema"
	protocol "github.com/viant/mcp-protocol/server"
	mcpserver "github.com/viant/mcp/server"
	"github.com/xuri/excelize/v2"
	_ "modernc.org/sqlite"
)

type outputPersistenceProvider struct {
	url   string
	paths bool
}

func (p *outputPersistenceProvider) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	policy := mcpcfg.OutputArtifact{Name: "spreadsheet-editor.xlsx", MimeType: "application/octet-stream"}
	if p.paths {
		policy.NamePath = "/structuredContent/file/name"
		policy.MimeTypePath = "/structuredContent/file/mimeType"
		policy.BytesPath = "/structuredContent/file/data"
		policy.Encoding = "base64"
	}
	return &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{ProtocolVersion: schema.LatestProtocolVersion, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: p.url}}}, ToolsListVisibility: mcpcfg.ToolsListVisibilityPublic, OutputArtifacts: map[string]mcpcfg.OutputArtifact{"Download": policy}}, nil
}

func TestOutputArtifactNativePersistenceAndTraceContainOnlyMetadata(t *testing.T) {
	for _, paths := range []bool{false, true} {
		label := "standard-blob"
		if paths {
			label = "declarative-paths"
		}
		t.Run(label, func(t *testing.T) { testFakeCampaign470750ArtifactRoundTrip(t, paths) })
	}
}

// This is an owned synthetic workbook and mock writer, not live campaign data.
func testFakeCampaign470750ArtifactRoundTrip(t *testing.T, pathMode bool) {
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	root := t.TempDir()
	t.Setenv("AGENTLY_MCP_SERVERS", "service")
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.Join(root, "artifacts", "${userID}"))
	ctx := authctx.WithUserInfo(requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "output-artifact-chat", TurnID: "output-artifact-turn", ParentMessageID: "output-parent"}), &authctx.UserInfo{Subject: "output-owner"})
	server, err := native.New(ctx, native.Options{WorkspaceRoot: root})
	require.NoError(t, err)
	defer server.Shutdown(context.Background())
	db, err := sql.Open("sqlite", filepath.Join(root, "db", "agently-core.db"))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec("INSERT INTO conversation(id,created_by_user_id) VALUES('output-artifact-chat','output-owner')")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO turn(id,conversation_id,status) VALUES('output-artifact-turn','output-artifact-chat','running')")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO message(id,conversation_id,role,type,content) VALUES('output-parent','output-artifact-chat','user','text','download workbook')")
	require.NoError(t, err)
	conv, err := conversation.New(ctx, server)
	require.NoError(t, err)
	workbook := excelize.NewFile()
	defer workbook.Close()
	require.NoError(t, workbook.SetCellValue("Sheet1", "A1", 470750))
	_, err = workbook.NewSheet("Sites")
	require.NoError(t, err)
	require.NoError(t, workbook.SetCellValue("Sites", "A1", "owned.example"))
	buffer, err := workbook.WriteToBuffer()
	require.NoError(t, err)
	data := buffer.Bytes()
	writerAccepted := make(chan []byte, 1)
	rpcServer, err := mcpserver.New(mcpserver.WithStreamableURI("/mcp"), mcpserver.WithNewHandler(func(ctx context.Context, n transport.Notifier, l logger.Logger, operations protocolclient.Operations) (protocol.Handler, error) {
		handler := protocol.NewDefaultHandler(n, l, operations)
		handler.Registry.RegisterToolWithSchema("Download", "Download generated workbook", schema.ToolInputSchema{Type: "object"}, nil, func(ctx context.Context, request *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
			_, callbackError := operations.CreateMessage(ctx, &jsonrpc.TypedRequest[*schema.CreateMessageRequest]{Request: &schema.CreateMessageRequest{}})
			if callbackError == nil {
				return nil, jsonrpc.NewInternalError("output callback fence missing", nil)
			}
			mime := "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
			untrustedURI := "urn:generated-workbook:" + base64.StdEncoding.EncodeToString(data) + ":signed=URI-EGRESS-SENTINEL"
			if pathMode {
				return &schema.CallToolResult{StructuredContent: map[string]interface{}{"file": map[string]interface{}{"name": "fresh-fake-campaign470750.xlsx", "mimeType": mime, "data": base64.StdEncoding.EncodeToString(data), "uri": untrustedURI}, "duplicate": base64.StdEncoding.EncodeToString(data)}, Content: []schema.CallToolResultContentElem{schema.TextContent{Type: "text", Text: base64.StdEncoding.EncodeToString(data)}}}, nil
			}
			return &schema.CallToolResult{Content: []schema.CallToolResultContentElem{schema.EmbeddedResource{Type: "resource", Resource: schema.EmbeddedResourceResource{Uri: untrustedURI, MimeType: &mime, Blob: base64.StdEncoding.EncodeToString(data)}}}}, nil
		})
		handler.Registry.RegisterToolWithSchema("Upload", "Fake authoritative campaign writer", schema.ToolInputSchema{Type: "object", Properties: schema.ToolInputSchemaProperties{"body": map[string]interface{}{"type": "string"}, "campaignId": map[string]interface{}{"type": "integer"}}}, nil, func(ctx context.Context, request *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
			if fmt.Sprint(request.Params.Arguments["campaignId"]) != "470750" {
				return nil, jsonrpc.NewInvalidParamsError("fake campaign target mismatch", nil)
			}
			encoded, ok := request.Params.Arguments["body"].(string)
			if !ok {
				return nil, jsonrpc.NewInvalidParamsError("binary input required", nil)
			}
			payload, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, jsonrpc.NewInvalidParamsError("binary input invalid", nil)
			}
			writerAccepted <- payload
			return &schema.CallToolResult{StructuredContent: map[string]interface{}{"accepted": true, "campaignId": 470750}}, nil
		})
		return handler, nil
	}))
	require.NoError(t, err)
	rpcServer.UseStreamableHTTP(true)
	peer := httptest.NewServer(rpcServer.HTTP(context.Background(), "").Handler)
	defer peer.Close()
	var callbacksMu sync.Mutex
	var callbacks []*clienthandler.Handler
	mgr, err := manager.New(&outputPersistenceProvider{url: peer.URL + "/mcp", paths: pathMode}, manager.WithHandlerFactory(func() protocolclient.Handler {
		h := clienthandler.New(nil, nil)
		callbacksMu.Lock()
		callbacks = append(callbacks, h)
		callbacksMu.Unlock()
		return h
	}))
	require.NoError(t, err)
	defer mgr.CloseConversation("output-artifact-chat")
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	reg.Initialize(ctx)
	var traceOffset int64
	if info, err := os.Stat("/tmp/agently-debug.log"); err == nil {
		traceOffset = info.Size()
	}
	call, _, err := toolexec.ExecuteToolStep(ctx, reg, toolexec.StepInfo{ID: "output-artifact-call", Name: "service/Download", Args: map[string]interface{}{"jobId": "owned-job"}}, conv)
	require.NoError(t, err)
	require.Contains(t, call.Result, "downloadURI")
	var metadata struct {
		Resources []struct {
			URI string `json:"uri"`
		} `json:"resources"`
	}
	require.NoError(t, json.Unmarshal([]byte(call.Result), &metadata))
	require.Len(t, metadata.Resources, 1)
	published, err := scratchpad.New().ReadArtifactPayload(ctx, metadata.Resources[0].URI)
	require.NoError(t, err)
	require.True(t, bytes.Equal(data, published), "published workbook changed")
	reopened, err := excelize.OpenReader(bytes.NewReader(published))
	require.NoError(t, err)
	require.Equal(t, []string{"Sheet1", "Sites"}, reopened.GetSheetList())
	require.NoError(t, reopened.Close())
	descriptor, err := scratchpad.New().DescribeArtifact(ctx, metadata.Resources[0].URI)
	require.NoError(t, err)
	require.Empty(t, descriptor.SourceURI)
	request := httptest.NewRequest(http.MethodGet, "/v1/artifacts/"+descriptor.ID, nil).WithContext(ctx)
	response := httptest.NewRecorder()
	sdk.NewHandler(nil).ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.True(t, bytes.Equal(data, response.Body.Bytes()), "authenticated download changed workbook")
	macro := "${artifact[" + descriptor.ID + "].payload;xform=base64}"
	originalArgs := map[string]interface{}{"campaignId": 470750, "body": macro}
	uploaded, _, err := toolexec.ExecuteToolStep(ctx, reg, toolexec.StepInfo{ID: "fake-campaign470750-upload", Name: "service/Upload", Args: originalArgs}, conv)
	require.NoError(t, err)
	require.Equal(t, macro, originalArgs["body"])
	require.Equal(t, macro, uploaded.Arguments["body"])
	select {
	case accepted := <-writerAccepted:
		require.True(t, bytes.Equal(data, accepted), "fake authoritative writer received different workbook")
	case <-time.After(time.Second):
		t.Fatal("fake writer did not receive upload")
	}
	publicDescriptor, err := json.Marshal(descriptor)
	require.NoError(t, err)
	require.False(t, bytes.Contains(publicDescriptor, []byte("URI-EGRESS-SENTINEL")), "public descriptor leaked remote URI")
	inspect, err := resources.New(nil).Method("inspect")
	require.NoError(t, err)
	var inspected resources.InspectOutput
	require.NoError(t, inspect(ctx, &resources.InspectInput{URI: descriptor.URI}, &inspected))
	inspection, err := json.Marshal(inspected)
	require.NoError(t, err)
	require.False(t, bytes.Contains(inspection, []byte("URI-EGRESS-SENTINEL")), "resources:inspect leaked remote URI")
	require.False(t, bytes.Contains(inspection, []byte(base64.StdEncoding.EncodeToString(data)[:32])), "resources:inspect leaked payload echo")
	extract, err := resources.New(nil).Method("read")
	require.NoError(t, err)
	var extracted resources.ReadOutput
	require.NoError(t, extract(ctx, &resources.ReadInput{URI: descriptor.URI, Representation: "table", Select: &resources.ResourceSelection{Sheet: "Sites", Range: "A1:A1"}, Limits: &resources.ResourceLimits{MaxRows: 1}}, &extracted))
	require.NotNil(t, extracted.Table)
	require.Contains(t, fmt.Sprint(extracted.Table.Rows), "owned.example", "captured URI remains available for explicit bounded extraction")
	callbacksMu.Lock()
	isolatedCallback := callbacks[len(callbacks)-1]
	callbacksMu.Unlock()
	_, lateCallbackError := isolatedCallback.CreateMessage(ctx, nil)
	require.NotNil(t, lateCallbackError)
	require.True(t, strings.Contains(lateCallbackError.Message, "artifact payload"), "isolated callback remains fenced after transport closes")
	encodedPrefix := base64.StdEncoding.EncodeToString(data)[:32]
	require.False(t, bytes.Contains([]byte(call.Result), []byte(encodedPrefix)), "model-facing result contains binary output")
	require.False(t, bytes.Contains([]byte(call.Result), []byte("URI-EGRESS-SENTINEL")), "model-facing result leaked remote URI")
	rows, err := db.Query("SELECT inline_body FROM call_payload")
	require.NoError(t, err)
	responseFound := false
	for rows.Next() {
		var body []byte
		require.NoError(t, rows.Scan(&body))
		require.False(t, bytes.Contains(body, []byte(encodedPrefix)), "database tool payload contains binary output")
		require.False(t, bytes.Contains(body, []byte("URI-EGRESS-SENTINEL")), "tool_response leaked remote URI")
		if bytes.Contains(body, []byte("downloadURI")) {
			responseFound = true
		}
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.True(t, responseFound)
	rows, err = db.Query("SELECT content FROM message WHERE content IS NOT NULL")
	require.NoError(t, err)
	for rows.Next() {
		var content string
		require.NoError(t, rows.Scan(&content))
		require.False(t, bytes.Contains([]byte(content), []byte(encodedPrefix)), "transcript contains binary output")
		require.False(t, bytes.Contains([]byte(content), []byte("URI-EGRESS-SENTINEL")), "transcript leaked remote URI")
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	trace, err := os.Open("/tmp/agently-debug.log")
	require.NoError(t, err)
	defer trace.Close()
	_, err = trace.Seek(traceOffset, 0)
	require.NoError(t, err)
	var tail bytes.Buffer
	_, err = tail.ReadFrom(trace)
	require.NoError(t, err)
	require.False(t, bytes.Contains(tail.Bytes(), []byte(encodedPrefix)), "debug trace contains binary output")
	require.False(t, bytes.Contains(tail.Bytes(), []byte("URI-EGRESS-SENTINEL")), "debug trace leaked remote URI")
}
