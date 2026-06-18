//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/viant/agently-core/app/store/data"
	convwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
	msgwrite "github.com/viant/agently-core/pkg/agently/message/write"
	turnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
)

const defaultWorkspace = "/Users/awitas/go/src/github.com/viant-internal/steward_ai/deployment/steward"
const ownerSubject = "awitas_viant_devtest"

func main() {
	ctx := context.Background()
	workspace := defaultWorkspace
	if len(os.Args) > 1 && os.Args[1] != "" {
		workspace = os.Args[1]
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		fatal(err)
	}
	dao, err := data.NewDatlyFromWorkspace(ctx, workspace)
	if err != nil {
		fatal(err)
	}
	store := data.NewService(dao)

	now := time.Now().UTC()
	convID := "mobile-dashboard-" + uuid.NewString()
	turnID := "turn-" + uuid.NewString()
	userMsgID := "msg-" + uuid.NewString()
	assistantMsgID := "msg-" + uuid.NewString()

	conv := convwrite.NewMutableConversationView()
	conv.SetId(convID)
	conv.SetTitle("Mobile dashboard API verification")
	conv.SetCreatedAt(now)
	conv.SetUpdatedAt(now)
	conv.SetLastActivity(now)
	conv.SetCreatedByUserID(ownerSubject)
	conv.SetVisibility("private")
	conv.SetShareable(0)
	conv.SetStatus("succeeded")
	conv.SetMetadata(`{"verification":"mobile-dashboard-backward-compat","source":"seed_mobile_dashboard.go"}`)

	turn := turnwrite.NewMutableTurnView()
	turn.SetId(turnID)
	turn.SetConversationID(convID)
	turn.SetCreatedAt(now.Add(100 * time.Millisecond))
	turn.SetOrigin("user")
	turn.SetStatus("succeeded")
	turn.SetStartedByMessageID(userMsgID)

	user := msgwrite.NewMutableMessageView()
	user.SetId(userMsgID)
	user.SetConversationID(convID)
	user.SetTurnID(turnID)
	user.SetSequence(1)
	user.SetCreatedAt(now.Add(200 * time.Millisecond))
	user.SetCreatedByUserID(ownerSubject)
	user.SetRole("user")
	user.SetType("text")
	user.SetStatus("completed")
	user.SetContent("Show mobile dashboard backward compatibility proof")
	user.SetPhase("final")
	user.SetInterim(0)

	assistant := msgwrite.NewMutableMessageView()
	assistant.SetId(assistantMsgID)
	assistant.SetConversationID(convID)
	assistant.SetTurnID(turnID)
	assistant.SetSequence(2)
	assistant.SetCreatedAt(now.Add(300 * time.Millisecond))
	assistant.SetCreatedByUserID(ownerSubject)
	assistant.SetRole("assistant")
	assistant.SetType("text")
	assistant.SetStatus("completed")
	assistant.SetContent(dashboardMarkdown())
	assistant.SetPhase("final")
	assistant.SetInterim(0)

	if _, err := store.PatchConversations(ctx, []*convwrite.MutableConversationView{conv}); err != nil {
		fatal(fmt.Errorf("patch conversation: %w", err))
	}
	if _, err := store.PatchTurns(ctx, []*turnwrite.MutableTurnView{turn}); err != nil {
		fatal(fmt.Errorf("patch turn: %w", err))
	}
	if _, err := store.PatchMessages(ctx, []*msgwrite.MutableMessageView{user, assistant}); err != nil {
		fatal(fmt.Errorf("patch messages: %w", err))
	}

	out, _ := json.MarshalIndent(map[string]string{
		"conversationId": convID,
		"turnId":         turnID,
		"userMessageId":  userMsgID,
		"assistantId":    assistantMsgID,
		"workspace":      workspace,
	}, "", "  ")
	fmt.Println(string(out))
}

func dashboardMarkdown() string {
	return `Mobile dashboard backward compatibility proof.

` + "```forge-data\n" + `{"version":1,"id":"summary_metrics","data":[{"record_name":"Line 7288336","primary_value":1316.86,"secondary_ratio":0.17,"success_rate":4.02,"primary_status":"Ready"}]}
` + "```\n" + "```forge-ui\n" + `{"version":1,"title":"Mobile dashboard verification","subtitle":"Compact dashboard blocks","blocks":[{"id":"summary","kind":"dashboard.summary","dataSourceRef":"summary_metrics","metrics":["primary_value","secondary_ratio","success_rate"]},{"id":"primary-evidence","kind":"dashboard.table","title":"Primary evidence","dataSourceRef":"summary_metrics","columns":[{"key":"record_name","label":"Record","format":"text"},{"key":"primary_status","label":"Status","format":"text"},{"key":"primary_value","label":"Value","format":"currency"}]}]}
` + "```\n"
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
