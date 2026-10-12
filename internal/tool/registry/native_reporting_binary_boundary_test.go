package tool_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	convapi "github.com/viant/agently-core/app/store/conversation"
	native "github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	conversation "github.com/viant/agently-core/internal/service/conversation"
	registry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/mcp/manager"
	scratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/reporting"
	"github.com/viant/agently-core/service/shared/toolexec"
	"io"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The model-facing executor is real, including native transcript/payload writes.
// All artifact bytes and owners are synthetic and confined to this test.
func TestNativeReportingIncludeDataCannotEnterModelToolResult(t *testing.T) {
	for _, directFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("direct-first=%t", directFirst), func(t *testing.T) {
			testNativeReportingBinaryBoundary(t, directFirst)
		})
	}
}

func testNativeReportingBinaryBoundary(t *testing.T, directFirst bool) {
	root := t.TempDir()
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.Join(root, "scratchpad", "${userID}"))
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	ctx := authctx.WithUserInfo(requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "binary-test", TurnID: "binary-turn", ParentMessageID: "binary-parent"}), &authctx.UserInfo{Subject: "binary-owner"})
	server, err := native.New(ctx, native.Options{WorkspaceRoot: root})
	require.NoError(t, err)
	t.Cleanup(func() { server.Shutdown(context.Background()) })
	db, err := sql.Open("sqlite", filepath.Join(root, "db/agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec("INSERT INTO conversation(id,created_by_user_id) VALUES('binary-test','binary-owner')")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO turn(id,conversation_id,status) VALUES('binary-turn','binary-test','running')")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO message(id,conversation_id,role,type,content) VALUES('binary-parent','binary-test','user','text','Download synthetic artifact')")
	require.NoError(t, err)
	conv, err := conversation.New(ctx, server)
	require.NoError(t, err)
	data := append([]byte("%PDF-1.7 SYNTHETIC_BINARY_CONTEXT_SENTINEL\n"), bytes.Repeat([]byte{0, 255, 1, 254}, 1024)...)
	encoded := base64.StdEncoding.EncodeToString(data)
	store := reporting.NewMemoryStore()
	now := time.Now().UTC()
	require.NoError(t, store.CreateJob(ctx, &reporting.ExportJob{JobID: "synthetic-job", OwnerID: "binary-owner", ArtifactRef: "synthetic-report", Format: reporting.ExportFormatPDF, Status: reporting.JobStatusSucceeded, ArtifactID: "synthetic-artifact", CompletedAt: &now}))
	require.NoError(t, store.PutArtifact(ctx, &reporting.Artifact{ArtifactID: "synthetic-artifact", JobID: "synthetic-job", OwnerID: "binary-owner", ArtifactRef: "synthetic-report", Format: reporting.ExportFormatPDF, ContentType: "application/pdf", Data: data, CreatedAt: now}))
	reportingService := reporting.New(reporting.Options{Store: store})
	trusted, err := reportingService.GetArtifact(ctx, "synthetic-artifact")
	require.NoError(t, err)
	require.Equal(t, data, trusted.Data, "trusted service download must retain original bytes")
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(reportingService))
	definition, ok := reg.GetDefinitionWithContext(ctx, "reporting:get_artifact")
	require.True(t, ok)
	require.NotNil(t, definition)
	// Direct SDK/UI execution retains bytes even with conversation/turn context.
	directArtifact := *trusted
	if directFirst {
		direct, err := reg.Execute(ctx, "reporting:get_artifact", map[string]interface{}{"artifactId": "synthetic-artifact", "includeData": true})
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal([]byte(direct), &directArtifact))
		require.Equal(t, data, directArtifact.Data)
	}
	metadata, err := reg.Execute(ctx, "reporting:get_artifact", map[string]interface{}{"artifactId": "synthetic-artifact", "includeData": false})
	require.NoError(t, err)
	var metadataArtifact reporting.Artifact
	require.NoError(t, json.Unmarshal([]byte(metadata), &metadataArtifact))
	require.Empty(t, metadataArtifact.Data)
	require.Equal(t, directArtifact.ArtifactID, metadataArtifact.ArtifactID)
	require.Equal(t, directArtifact.SourceURL, metadataArtifact.SourceURL)
	var logOffset int64
	if info, err := os.Stat("/tmp/agently-debug.log"); err == nil {
		logOffset = info.Size()
	}
	opID := fmt.Sprintf("binary-causal-%d", time.Now().UnixNano())
	call, _, err := toolexec.ExecuteToolStep(ctx, reg, toolexec.StepInfo{ID: opID, Name: "reporting:get_artifact", Args: map[string]interface{}{"artifactId": "synthetic-artifact", "includeData": true}}, conv)
	require.NoError(t, err)
	t.Logf("model-facing result bytes=%d containsEncodedBinary=%t", len(call.Result), strings.Contains(call.Result, encoded))
	if strings.Contains(call.Result, encoded) {
		t.Error("binary reached model-facing tool result")
	}
	var content string
	require.NoError(t, db.QueryRow("SELECT COALESCE(content,'') FROM message WHERE id=?", call.ResultMessageID).Scan(&content))
	t.Logf("native message bytes=%d containsEncodedBinary=%t", len(content), strings.Contains(content, encoded))
	if strings.Contains(content, encoded) {
		t.Error("binary reached stored tool transcript content")
	}
	var payload, compression string
	require.NoError(t, db.QueryRow("SELECT COALESCE(p.inline_body,''), COALESCE(p.compression,'') FROM tool_call t JOIN call_payload p ON p.id=t.response_payload_id WHERE t.op_id=?", opID).Scan(&payload, &compression))
	payload = convapi.DecodeInlineBody(payload, compression)
	t.Logf("decoded response payload bytes=%d containsEncodedBinary=%t", len(payload), strings.Contains(payload, encoded))
	if strings.Contains(payload, encoded) {
		t.Error("binary reached stored response payload")
	}
	var modelArtifact reporting.Artifact
	require.NoError(t, json.Unmarshal([]byte(call.Result), &modelArtifact))
	require.Equal(t, directArtifact.ArtifactID, modelArtifact.ArtifactID)
	require.Equal(t, directArtifact.ContentType, modelArtifact.ContentType)
	require.Equal(t, directArtifact.SourceURL, modelArtifact.SourceURL)
	require.Empty(t, modelArtifact.Data)
	// A trusted download after recording must retain exact original bytes.
	secondDirect, err := reg.Execute(ctx, "reporting:get_artifact", map[string]interface{}{"artifactId": "synthetic-artifact", "includeData": true})
	require.NoError(t, err)
	var afterModel reporting.Artifact
	require.NoError(t, json.Unmarshal([]byte(secondDirect), &afterModel))
	require.Equal(t, data, afterModel.Data)

	artifacts, err := store.ListArtifacts(ctx)
	require.NoError(t, err)
	require.Len(t, artifacts, 1, "reading metadata must not create another reporting artifact")
	// Inspect only this test's own newly appended trace entry, never dump logs.
	logFile, err := os.Open("/tmp/agently-debug.log")
	require.NoError(t, err)
	defer logFile.Close()
	_, err = logFile.Seek(logOffset, io.SeekStart)
	require.NoError(t, err)
	tail, err := io.ReadAll(logFile)
	require.NoError(t, err)
	found := false
	for _, line := range bytes.Split(tail, []byte("\n")) {
		var entry map[string]interface{}
		if json.Unmarshal(line, &entry) != nil || entry["opID"] != opID {
			continue
		}
		found = true
		head, _ := entry["resultHead"].(string)
		if strings.Contains(head, encoded[:64]) {
			t.Error("binary reached trace result head")
		}
	}
	require.True(t, found, "actual recorded tool trace must be checked")
}
