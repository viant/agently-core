//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viant/agently-core/app/store/data"
	convwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
	msgwrite "github.com/viant/agently-core/pkg/agently/message/write"
	turnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
)

func main() {
	workspaceFlag := flag.String("workspace", os.Getenv("AGENTLY_MOBILE_SEED_WORKSPACE"), "workspace root containing the target Agently datastore")
	ownerFlag := flag.String("owner", os.Getenv("AGENTLY_MOBILE_SEED_OWNER"), "created_by user/subject for the seeded verification conversation")
	printMarkdown := flag.Bool("print-markdown", false, "print the seeded assistant markdown and exit without writing a workspace")
	flag.Parse()

	if *printMarkdown {
		fmt.Print(dashboardMarkdown())
		return
	}

	ctx := context.Background()
	workspace := strings.TrimSpace(*workspaceFlag)
	if workspace == "" && flag.NArg() > 0 {
		workspace = strings.TrimSpace(flag.Arg(0))
	}
	if workspace == "" {
		fatal(fmt.Errorf("workspace is required: pass -workspace or set AGENTLY_MOBILE_SEED_WORKSPACE"))
	}
	ownerSubject := strings.TrimSpace(*ownerFlag)
	if ownerSubject == "" {
		fatal(fmt.Errorf("owner is required: pass -owner or set AGENTLY_MOBILE_SEED_OWNER"))
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
	dataBlock := mustJSON(map[string]any{
		"version": 1,
		"id":      "summary_metrics",
		"data": []map[string]any{
			{
				"record_name":     "Sample display record",
				"channel":         "display",
				"primary_value":   1316.86,
				"secondary_ratio": 0.17,
				"success_rate":    4.02,
				"primary_status":  "Ready",
			},
			{
				"record_name":     "Sample CTV record",
				"channel":         "ctv",
				"primary_value":   894.42,
				"secondary_ratio": 0.11,
				"success_rate":    3.47,
				"primary_status":  "Review",
			},
		},
	})
	uiBlock := mustJSON(map[string]any{
		"version":  1,
		"title":    "Mobile dashboard verification",
		"subtitle": "Compact dashboard blocks",
		"blocks": []map[string]any{
			{
				"id":            "summary",
				"kind":          "dashboard.summary",
				"dataSourceRef": "summary_metrics",
				"metrics":       []string{"primary_value", "secondary_ratio", "success_rate"},
			},
			{
				"id":    "dashboard-filter-controls",
				"kind":  "dashboard.filters",
				"title": "Filter controls",
				"items": []map[string]any{
					{
						"id":       "channel",
						"label":    "Channel",
						"field":    "channel",
						"multiple": true,
						"options": []map[string]any{
							{"label": "Display", "value": "display", "default": true},
							{"label": "CTV", "value": "ctv"},
						},
					},
				},
			},
			{
				"id":            "primary-evidence",
				"kind":          "dashboard.table",
				"title":         "Primary evidence",
				"dataSourceRef": "summary_metrics",
				"columns": []map[string]any{
					{"key": "record_name", "label": "Record", "format": "text"},
					{"key": "channel", "label": "Channel", "format": "text"},
					{"key": "primary_status", "label": "Status", "format": "text"},
					{"key": "primary_value", "label": "Value", "format": "currency"},
				},
			},
		},
	})
	return `Mobile dashboard backward compatibility proof.

` + "```forge-data\n" + dataBlock + "\n```\n" +
		"```forge-ui\n" + uiBlock + "\n```\n"
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
