package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/internal/auth/mcpauth"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/elicitation"
	"github.com/viant/agently-core/service/elicitation/router"
)

type mcpPromptRecorder struct {
	apiconv.Client
	message *apiconv.MutableMessage
	payload *apiconv.MutablePayload
	insert  func(context.Context, *apiconv.MutableMessage) error
}

func (c *mcpPromptRecorder) PatchPayload(_ context.Context, p *apiconv.MutablePayload) error {
	c.payload = p
	return nil
}

func (c *mcpPromptRecorder) PatchMessage(ctx context.Context, m *apiconv.MutableMessage) error {
	// Match the roles permitted by the production message schema.
	switch m.Role {
	case "system", "user", "assistant", "tool", "chain":
	default:
		return fmt.Errorf("message role constraint rejected %q", m.Role)
	}
	c.message = m
	if c.insert != nil {
		return c.insert(ctx, m)
	}
	return nil
}

func (c *mcpPromptRecorder) GetConversation(_ context.Context, id string, _ ...apiconv.Option) (*apiconv.Conversation, error) {
	return &apiconv.Conversation{Id: id}, nil
}

func (c *mcpPromptRecorder) GetMessageByElicitation(_ context.Context, _, _ string) (*apiconv.Message, error) {
	accepted := "accepted"
	return &apiconv.Message{Status: &accepted}, nil
}

func checkMCPAuthPrompt(t *testing.T, recorder *mcpPromptRecorder) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	ctx = requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "conversation", TurnID: "turn"})
	blocker := &mcpAuthBlocker{elicitation: elicitation.New(recorder, nil, router.New(), nil)}
	require.NoError(t, blocker.AwaitMCPAuth(ctx, &mcpauth.LinkRequiredError{ServerName: "asana"}))
	require.NotNil(t, recorder.message)
	require.Equal(t, "system", recorder.message.Role)
	require.Equal(t, "control", recorder.message.Type)
	require.Equal(t, "pending", *recorder.message.Status)
	var prompt execution.Elicitation
	require.NoError(t, json.Unmarshal(*recorder.payload.InlineBody, &prompt))
	require.Equal(t, "mcp_oauth", string(prompt.Mode))
	require.Equal(t, "/v1/api/auth/mcp/asana/initiate", prompt.Url)
	require.NotEmpty(t, prompt.ElicitationId)
}

func TestMCPAuthBlockerUsesSupportedSystemRole(t *testing.T) {
	checkMCPAuthPrompt(t, &mcpPromptRecorder{})
}

func TestMCPAuthBlockerMySQLRoleConstraint(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_MCP_ROLE_DSN")
	if dsn == "" || os.Getenv("AGENTLY_TEST_MYSQL_MCP_ROLE_OWNED") != "1" {
		t.Skip("requires an explicitly owned MySQL test database")
	}
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	defer db.Close()
	_, file, _, _ := runtime.Caller(0)
	ddl, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../script/mysql/schema_versioned_steward.ddl"))
	require.NoError(t, err)
	roleCheck := regexp.MustCompile(`(?m)role\s+VARCHAR\(255\) NOT NULL CHECK \((role IN \([^\n]+\))\),`).FindSubmatch(ddl)
	require.Len(t, roleCheck, 2, "use the actual Steward role constraint")
	const table = "mcp_oauth_message_role_test"
	_, err = db.Exec("CREATE TABLE " + table + " (id VARCHAR(255) PRIMARY KEY, role VARCHAR(255) NOT NULL, type VARCHAR(255) NOT NULL, CONSTRAINT mcp_role_chk CHECK (" + string(roleCheck[1]) + "))")
	require.NoError(t, err)
	defer db.Exec("DROP TABLE " + table)
	_, err = db.Exec("INSERT INTO " + table + " VALUES ('invalid', 'control', 'control')")
	require.ErrorContains(t, err, "3819", "fixture must reproduce the production constraint violation")
	checkMCPAuthPrompt(t, &mcpPromptRecorder{insert: func(ctx context.Context, m *apiconv.MutableMessage) error {
		_, err := db.ExecContext(ctx, "INSERT INTO "+table+" (id,role,type) VALUES (?,?,?)", m.Id, m.Role, m.Type)
		return err
	}})
	var role, messageType string
	require.NoError(t, db.QueryRow("SELECT role,type FROM "+table).Scan(&role, &messageType))
	require.Equal(t, "system", role)
	require.Equal(t, "control", messageType)
}
