package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	aguistore "github.com/viant/agently-core/app/store/agui"
)

// A goal mutation and successful protocol result commit in one Datly root
// transaction. A rolled-back command gets a separate terminal error journal;
// a post-commit notification failure must never overwrite the committed result.
func runAGUIResourceWorker(ctx context.Context, client Client, store aguistore.Store, record *aguistore.Run, operation string, payload json.RawMessage) {
	claimed, err := store.Claim(ctx, record.Principal, record.ThreadID, record.RunID, record.Revision, uuid.NewString(), time.Minute)
	if err != nil {
		log.Printf("AG-UI goal claim failed: %v", err)
		return
	}
	if operation == "feed.subscribe" {
		if err := runAGUIFeedSubscription(ctx, client, store, claimed, payload); err != nil {
			log.Printf("AG-UI feed subscription failed: %v", err)
		}
		return
	}
	if operation == "goal.subscribe" {
		if err := runAGUIGoalSubscription(ctx, client, store, claimed, payload); err != nil {
			log.Printf("AG-UI goal subscription failed: %v", err)
		}
		return
	}
	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	if operation == "approval.decide" {
		if coordinator, ok := client.(interface {
			aguiRunApprovalCommand(context.Context, aguistore.Store, *aguistore.Run, json.RawMessage) error
		}); ok {
			err = coordinator.aguiRunApprovalCommand(commandCtx, store, claimed, payload)
		} else {
			err = fmt.Errorf("durable approval coordinator unavailable")
		}
	} else if operation == "conversation.bootstrap" {
		err = runAGUIConversationBootstrap(commandCtx, client, store, claimed, payload)
	} else if strings.HasPrefix(operation, "state.") {
		err = runAGUIStateCommand(commandCtx, store, claimed, operation, payload)
	} else if strings.HasPrefix(operation, "goal.") {
		_, err = executeAGUIGoalTransaction(commandCtx, client, store, claimed, operation, payload)
	} else {
		err = runAGUIWorkspaceCommand(commandCtx, client, store, claimed, operation, payload)
	}
	cancel()
	if err == nil {
		return
	}
	latest, readErr := store.GetRun(ctx, record.Principal, record.ThreadID, record.RunID)
	if readErr != nil {
		log.Printf("AG-UI goal failed: %v; cannot read outcome: %v", err, readErr)
		return
	}
	if aguiRunTerminal(latest.Status) {
		log.Printf("AG-UI goal committed with notification error: %v", err)
		return
	}
	var events []json.RawMessage
	if latest.LastSequence == 0 {
		events = append(events, rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": record.ThreadID, "runId": record.RunID, "protocolVersion": "1.0"}))
	}
	events = append(events, rawAGUI(map[string]any{"type": "RUN_ERROR", "message": err.Error(), "code": "COMMAND_FAILED"}))
	if _, journalErr := store.Append(ctx, record.Principal, record.ThreadID, record.RunID, latest.Revision, events, &aguistore.Change{LeaseOwner: claimed.LeaseOwner}); journalErr != nil {
		log.Printf("AG-UI goal failed: %v; cannot journal error: %v", err, journalErr)
	}
}

// Filesystem operations cannot commit with the database journal. Once dispatch
// started, an abandoned worker is reported as uncertain rather than repeated.
func runAGUIWorkspaceCommand(ctx context.Context, client Client, store aguistore.Store, record *aguistore.Run, operation string, payload json.RawMessage) error {
	if record.LastSequence > 0 {
		return fmt.Errorf("workspace command was interrupted after dispatch; inspect the resource before retrying with a new runId")
	}
	started, err := store.Append(ctx, record.Principal, record.ThreadID, record.RunID, record.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": record.ThreadID, "runId": record.RunID, "protocolVersion": "1.0"})}, &aguistore.Change{LeaseOwner: record.LeaseOwner})
	if err != nil {
		return err
	}
	var result any
	handled := true
	if strings.HasPrefix(operation, "run.") {
		result, err = dispatchAGUIRunCommand(ctx, client, store, record, operation, payload)
	} else {
		result, handled, err = dispatchAGUIWorkspace(ctx, client, record.ConversationID, operation, payload)
	}
	if err != nil {
		return err
	}
	if !handled {
		return fmt.Errorf("unsupported workspace operation %q", operation)
	}
	_, err = store.Append(ctx, record.Principal, record.ThreadID, record.RunID, started.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": record.ThreadID, "runId": record.RunID, "result": result, "outcome": map[string]any{"type": "success"}})}, &aguistore.Change{LeaseOwner: record.LeaseOwner})
	return err
}
