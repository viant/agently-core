package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	scratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	"github.com/viant/agently-core/runtime/mcpapps"
	schema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

func binaryOutput(data []byte) *schema.CallToolResult {
	mime := "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	return &schema.CallToolResult{Content: []schema.CallToolResultContentElem{schema.EmbeddedResource{Type: "resource", Resource: schema.EmbeddedResourceResource{Uri: "opaque:generated-workbook", MimeType: &mime, Blob: base64.StdEncoding.EncodeToString(data)}}}}
}

type outputCaptureManager struct {
	artifactRetryManager
	policy        *mcpcfg.MCPClient
	isolatedCalls int
}

func (m *outputCaptureManager) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return m.policy, nil
}
func (m *outputCaptureManager) NewSensitiveClient(ctx context.Context, conversation, server string) (mcpclient.Interface, error) {
	m.isolatedCalls++
	return m.sensitive, nil
}

func TestOutputArtifactRegistryCapturesBeforeCompositionAndCache(t *testing.T) {
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.Join(t.TempDir(), "${userID}"))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	data := []byte{'P', 'K', 0, 255, 128, 34, '\n'}
	result := binaryOutput(data)
	remote := result.Content[0].(schema.EmbeddedResource)
	remote.Resource.Uri = "urn:output:" + base64.StdEncoding.EncodeToString(data) + ":signed=URI-EGRESS-SENTINEL"
	result.Content[0] = remote
	result.Content = append(result.Content, schema.TextContent{Type: "text", Text: base64.StdEncoding.EncodeToString(data)})
	result.StructuredContent = map[string]interface{}{"File": data, "nonserializableTransient": func() {}}
	client := &artifactRetryClient{scriptedCallClient: scriptedCallClient{result: result}}
	mgr := &outputCaptureManager{artifactRetryManager: artifactRetryManager{scriptedReconnectManager: scriptedReconnectManager{clients: []mcpclient.Interface{&scriptedCallClient{}}}, sensitive: client}, policy: &mcpcfg.MCPClient{OutputArtifacts: map[string]mcpcfg.OutputArtifact{"Download": {Name: "workbook.xlsx"}}}}
	reg := newReconnectTestRegistry(&mgr.scriptedReconnectManager)
	reg.mgr, reg.recentTTL = mgr, time.Minute
	out, err := reg.Execute(ctx, "service/Download", map[string]interface{}{"jobId": "owned-job"})
	require.NoError(t, err)
	require.Equal(t, 1, mgr.isolatedCalls)
	require.Equal(t, 1, client.calls)
	require.True(t, client.options.NoRetry)
	require.NotContains(t, out, base64.StdEncoding.EncodeToString(data))
	require.NotContains(t, out, "File")
	require.NotContains(t, out, "URI-EGRESS-SENTINEL")
	var metadata struct {
		Resources []struct {
			URI         string `json:"uri"`
			DownloadURI string `json:"downloadURI"`
			Name        string `json:"name"`
		} `json:"resources"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &metadata))
	require.Len(t, metadata.Resources, 1)
	require.Equal(t, "workbook.xlsx", metadata.Resources[0].Name)
	require.Contains(t, metadata.Resources[0].DownloadURI, "/v1/artifacts/")
	actual, err := scratchpad.New().ReadArtifactPayload(ctx, metadata.Resources[0].URI)
	require.NoError(t, err)
	require.Equal(t, data, actual)
	descriptor, err := scratchpad.New().DescribeArtifact(ctx, metadata.Resources[0].URI)
	require.NoError(t, err)
	require.Empty(t, descriptor.SourceURI)
	for _, byKey := range reg.recentResults {
		for _, item := range byKey {
			require.NotContains(t, item.out, base64.StdEncoding.EncodeToString(data))
		}
	}
	// A host recorder must receive only the replaced metadata result too.
	mgr.policy.ToolsListVisibility = mcpcfg.ToolsListVisibilityPublic
	reg.cache["service/Download"] = &toolCacheEntry{def: llm.ToolDefinition{Name: "service/Download"}, mcpDef: schema.Tool{Name: "Download"}}
	host, capture := mcpapps.WithCapture(ctx, "service", "Download", "host-output")
	var recorded json.RawMessage
	host = mcpapps.WithRecorder(host, func(result json.RawMessage, _ string) error { recorded = append(recorded, result...); return nil })
	_, err = reg.Execute(host, "service/Download", map[string]interface{}{"jobId": "owned-job"})
	require.NoError(t, err)
	require.NotEmpty(t, recorded)
	require.NotContains(t, string(recorded), base64.StdEncoding.EncodeToString(data))
	require.NotContains(t, string(recorded), "File")
	require.NotContains(t, string(recorded), "URI-EGRESS-SENTINEL")
	snapshot, _ := capture.Snapshot()
	require.Equal(t, recorded, snapshot)
}

func TestOutputArtifactOptionalMIMEAndOccurrenceBounds(t *testing.T) {
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.Join(t.TempDir(), "${userID}"))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	result := binaryOutput([]byte("owned"))
	value := result.Content[0].(schema.EmbeddedResource)
	value.Resource.MimeType = nil
	result.Content[0] = value
	captured, err := captureOutputArtifacts(ctx, result, &mcpcfg.OutputArtifact{})
	require.NoError(t, err)
	resource := captured.StructuredContent.(map[string]interface{})["resources"].([]map[string]interface{})[0]
	require.Equal(t, "application/octet-stream", resource["mimeType"])
	for len(result.Content) <= 32 {
		result.Content = append(result.Content, value)
	}
	_, err = captureOutputArtifacts(ctx, result, &mcpcfg.OutputArtifact{})
	require.Error(t, err, "occurrence bound applies before publication")
}

func TestOutputArtifactRejectsUnsafeResultsWithoutExposingBytes(t *testing.T) {
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.Join(t.TempDir(), "${userID}"))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	for _, mutate := range []func(*schema.CallToolResult){
		func(r *schema.CallToolResult) {
			r.Content = []schema.CallToolResultContentElem{schema.ResourceLink{Type: "resource_link", Name: "remote", Uri: "https://private.invalid/workbook"}}
		},
		func(r *schema.CallToolResult) {
			r.Content = []schema.CallToolResultContentElem{schema.TextContent{Type: "text", Text: "private bytes"}}
		},
		func(r *schema.CallToolResult) {
			value := r.Content[0].(schema.EmbeddedResource)
			value.Resource.Blob = "private malformed bytes"
			r.Content[0] = value
		},
		func(r *schema.CallToolResult) {
			value := r.Content[0].(schema.EmbeddedResource)
			value.Resource.Text = "private bytes"
			r.Content[0] = value
		},
		func(r *schema.CallToolResult) {
			value := r.Content[0].(schema.EmbeddedResource)
			invalid := "invalid\r\nprivate bytes"
			value.Resource.MimeType = &invalid
			r.Content[0] = value
		},
		func(r *schema.CallToolResult) { flag := true; r.IsError = &flag },
	} {
		result := binaryOutput([]byte("private workbook bytes"))
		mutate(result)
		_, err := captureOutputArtifacts(ctx, result, &mcpcfg.OutputArtifact{})
		require.EqualError(t, err, "output artifact capture failed")
	}
	_, err := captureOutputArtifacts(context.Background(), binaryOutput([]byte("owned")), &mcpcfg.OutputArtifact{})
	require.Error(t, err)
}

func TestOutputArtifactExactPolicyAndTransportErrorDoNotReplayOrEcho(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	client := &artifactRetryClient{scriptedCallClient: scriptedCallClient{err: errors.New("connection reset by peer private workbook bytes")}}
	mgr := &outputCaptureManager{artifactRetryManager: artifactRetryManager{scriptedReconnectManager: scriptedReconnectManager{clients: []mcpclient.Interface{&scriptedCallClient{}}}, sensitive: client}, policy: &mcpcfg.MCPClient{OutputArtifacts: map[string]mcpcfg.OutputArtifact{"Download": {}}}}
	reg := newReconnectTestRegistry(&mgr.scriptedReconnectManager)
	reg.mgr = mgr
	policy, err := reg.outputArtifactPolicy(ctx, "service", "download")
	require.NoError(t, err)
	require.Nil(t, policy)
	_, err = reg.Execute(ctx, "service/Download", map[string]interface{}{})
	require.EqualError(t, err, "output artifact dispatch failed")
	require.Equal(t, 1, client.calls)
	require.Empty(t, mgr.reconnectCalls)
	_, err = reg.Execute(context.Background(), "service/Download", map[string]interface{}{})
	require.EqualError(t, err, "output artifact identity required")
	require.Equal(t, 1, client.calls)
}
