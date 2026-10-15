package reportingrun

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/viant/agently-core/app/store/native"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportsql "github.com/viant/agently-core/app/store/reporting/sql"
	conversationsql "github.com/viant/agently-core/internal/service/conversation"
	runs "github.com/viant/agently-core/service/reportingrun"
	"github.com/viant/datly/bootstrap/connector"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestHandlerMySQLOwnedConversationBeginsDurableRun(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN not set")
	}
	ctx := context.Background()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server, err := native.New(ctx, native.Options{Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn}}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(ctx)
	conv, err := conversationsql.New(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	store, err := reportsql.New(ctx, server, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := fmt.Sprintf("run-repro-%d", time.Now().UnixNano())
	id := owner + "-conversation"
	_, err = db.Exec("INSERT INTO conversation(id,created_by_user_id,visibility,status) VALUES(?,?,'private','succeeded')", id, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DELETE FROM report_run WHERE owner_id=?", owner)
	defer db.Exec("DELETE FROM conversation WHERE id=?", id)
	handler := New(runs.New(runs.Options{Store: store.(reportstore.RunClient)}), conv, false)
	result := postJSON(t, handler, routePrefix+"/begin", map[string]any{"uiRunRequestId": owner + "-request", "conversationId": id, "origin": "manual", "builderRef": "metricsCubeBuilder", "requestedParams": map[string]any{"from": "2026-10-01", "to": "2026-10-09"}}, owner)
	t.Logf("status=%d body=%s", result.Code, result.Body.String())
	if result.Code != http.StatusOK {
		t.Fail()
	}
}
