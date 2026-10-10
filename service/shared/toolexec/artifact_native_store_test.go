package toolexec_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	native "github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	conversation "github.com/viant/agently-core/internal/service/conversation"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/tool"
	approvalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	scratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/shared/toolexec"
	_ "modernc.org/sqlite"
)

type nativeArtifactRegistry struct {
	received []byte
	cfg      *asynccfg.Config
	calls    int
}

func (r *nativeArtifactRegistry) Definitions() []llm.ToolDefinition            { return nil }
func (r *nativeArtifactRegistry) MatchDefinition(string) []*llm.ToolDefinition { return nil }
func (r *nativeArtifactRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) {
	return &llm.ToolDefinition{Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"body": map[string]interface{}{"type": "string", "contentEncoding": "base64"}}}}, true
}
func (r *nativeArtifactRegistry) MustHaveTools([]string) ([]llm.Tool, error) { return nil, nil }
func (r *nativeArtifactRegistry) SetDebugLogger(io.Writer)                   {}
func (r *nativeArtifactRegistry) Initialize(context.Context)                 {}
func (r *nativeArtifactRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	r.calls++
	execution, _, err := tool.ResolveDispatchPayload(ctx, name, args)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(execution)
	if err != nil {
		return "", err
	}
	var input struct {
		Body []byte `json:"body"`
	}
	if err = json.Unmarshal(raw, &input); err != nil {
		return "", err
	}
	r.received = input.Body
	return `{"uploaded":true,"operationId":"owned-native-business-id"}`, nil
}
func TestArtifactMacroActualNativeSQLiteRequestPersistence(t *testing.T) {
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	root := t.TempDir()
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(root, "artifacts", "${userID}")))
	ctx := authctx.WithUserInfo(requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "owned-artifact-chat", TurnID: "owned-artifact-turn", ParentMessageID: "owned-parent"}), &authctx.UserInfo{Subject: "owned-user"})
	server, err := native.New(ctx, native.Options{WorkspaceRoot: root})
	require.NoError(t, err)
	defer server.Shutdown(context.Background())
	db, err := sql.Open("sqlite", filepath.Join(root, "db", "agently-core.db"))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec("INSERT INTO conversation(id,created_by_user_id) VALUES('owned-artifact-chat','owned-user')")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO turn(id,conversation_id,status) VALUES('owned-artifact-turn','owned-artifact-chat','running')")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO message(id,conversation_id,role,type,content) VALUES('owned-parent','owned-artifact-chat','user','text','owned request')")
	require.NoError(t, err)
	service, err := conversation.New(ctx, server)
	require.NoError(t, err)
	id := "2f1c171e-0fe9-4aa7-8f17-5eb826f72042"
	data := []byte("site\nowned.example\n")
	_, err = scratchpad.New().PublishArtifact(ctx, id, "sites.csv", "text/csv", "", bytes.NewReader(data))
	require.NoError(t, err)
	macro := "${artifact[" + id + "].payload;xform=base64}"
	registry := &nativeArtifactRegistry{}
	call, _, err := toolexec.ExecuteToolStep(ctx, registry, toolexec.StepInfo{ID: "owned-artifact-op", Name: "owned/upload", Args: map[string]interface{}{"body": macro}}, service)
	require.NoError(t, err)
	require.Equal(t, data, registry.received)
	require.Equal(t, macro, call.Arguments["body"])
	rows, err := db.Query("SELECT kind,inline_body FROM call_payload ORDER BY kind")
	require.NoError(t, err)
	defer rows.Close()
	requestFound := false
	for rows.Next() {
		var kind string
		var body []byte
		require.NoError(t, rows.Scan(&kind, &body))
		require.NotContains(t, string(body), "owned.example")
		require.NotContains(t, string(body), base64.StdEncoding.EncodeToString(data))
		if kind == "tool_request" {
			requestFound = true
			require.Contains(t, string(body), macro)
		}
	}
	require.NoError(t, rows.Err())
	require.True(t, requestFound)
	reviewContext := approvalqueue.WithState(ctx)
	approvalqueue.MarkTool(reviewContext, "owned/upload", &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue})
	reviewRegistry := &nativeArtifactRegistry{}
	_, _, err = toolexec.ExecuteToolStep(reviewContext, reviewRegistry, toolexec.StepInfo{ID: "owned-review-op", Name: "owned/upload", Args: map[string]interface{}{"body": macro}}, service)
	require.NoError(t, err)
	require.Empty(t, reviewRegistry.received)
	var reviewArguments []byte
	require.NoError(t, db.QueryRow("SELECT arguments FROM tool_approval_queue WHERE tool_name='owned/upload'").Scan(&reviewArguments))
	require.Contains(t, string(reviewArguments), macro)
	require.NotContains(t, string(reviewArguments), "owned.example")
	require.NotContains(t, string(reviewArguments), base64.StdEncoding.EncodeToString(data))
	registry.cfg = &asynccfg.Config{Run: asynccfg.RunConfig{Tool: "owned/upload", OperationIDPath: "operationId"}, Status: asynccfg.StatusConfig{Tool: "owned/status", OperationIDArg: "operationId"}}
	restartContext := toolexec.WithAsyncManager(ctx, asynccfg.NewManager())
	before := registry.calls
	_, _, resumeErr := toolexec.ExecuteToolStep(restartContext, registry, toolexec.StepInfo{ID: "owned-resume-op", Name: "owned/status", Args: map[string]interface{}{"operationId": "owned-native-business-id"}}, service)
	require.Error(t, resumeErr)
	require.Contains(t, resumeErr.Error(), "original server session unavailable")
	require.Equal(t, before, registry.calls, "restart-resume must fail before sending status to the provider")
	foreign := authctx.WithUserInfo(restartContext, &authctx.UserInfo{Subject: "foreign-user"})
	_, _, foreignErr := toolexec.ExecuteToolStep(foreign, registry, toolexec.StepInfo{ID: "owned-foreign-resume", Name: "owned/status", Args: map[string]interface{}{"operationId": "owned-native-business-id"}}, service)
	require.Error(t, foreignErr)
	require.Contains(t, foreignErr.Error(), "owner mismatch")
	require.Equal(t, before, registry.calls)
	configured := registry.cfg
	registry.cfg = nil
	_, _, err = toolexec.ExecuteToolStep(ctx, registry, toolexec.StepInfo{ID: "owned-second-origin", Name: "owned/upload", Args: map[string]interface{}{"body": macro}}, service)
	require.NoError(t, err)
	registry.cfg = configured
	before = registry.calls
	_, _, ambiguousErr := toolexec.ExecuteToolStep(restartContext, registry, toolexec.StepInfo{ID: "owned-ambiguous-resume", Name: "owned/status", Args: map[string]interface{}{"operationId": "owned-native-business-id"}}, service)
	require.Error(t, ambiguousErr)
	require.Contains(t, ambiguousErr.Error(), "linkage ambiguous")
	require.Equal(t, before, registry.calls)

}

func (r *nativeArtifactRegistry) AsyncConfig(name string) (*asynccfg.Config, bool) {
	return r.cfg, r.cfg != nil && (name == r.cfg.Run.Tool || name == r.cfg.Status.Tool)
}
