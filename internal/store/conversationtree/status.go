package conversationtree

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	msgread "github.com/viant/agently-core/internal/datly/message/read"
	contextread "github.com/viant/agently-core/internal/datly/reporting/context/read"
	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"
	reportrun "github.com/viant/agently-core/internal/datly/reporting/run/read"
	schedread "github.com/viant/agently-core/internal/datly/schedule/read"
	turnread "github.com/viant/agently-core/internal/datly/turn/read"
	legacyqueue "github.com/viant/agently-core/internal/datly/turnqueue/read"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

var ErrNonTerminal = errors.New("conversation graph contains unknown nonterminal activity")

var queueReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[legacyqueue.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/turnqueue/list"},
}

// ValidateNonTerminalStatuses mirrors the legacy rule: terminal, known active
// and empty statuses proceed to run-liveness checks. An unknown nonempty
// status blocks deletion only when the conversation still has activity.
func (d *Discoverer) ValidateNonTerminalStatuses(ctx context.Context, graph *Graph) error {
	if d == nil || d.Invoker == nil || d.OwnerID == nil {
		return fmt.Errorf("conversation graph reader is not configured")
	}
	if err := d.authorize(ctx, graph); err != nil {
		return err
	}
	if len(graph.Nodes) == 0 {
		return nil
	}
	ids := sortedMapKeys(graph.Nodes)
	activity := map[string]bool{}
	mark := func(id string) {
		if graph.Nodes[id] != nil {
			activity[id] = true
		}
	}
	turnQuery := &turnread.TurnRowsInput{}
	turnQuery.SetConversationIDs(ids)
	turns, err := d.turnRows(ctx, turnQuery, nil)
	if err != nil {
		return err
	}
	for _, row := range turns {
		if row != nil {
			mark(row.ConversationId)
		}
	}
	messageQuery := &msgread.MessagesInput{}
	messageQuery.SetConversationIds(ids)
	selector := state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: []string{"id", "conversation_id"}}}}
	messages, err := d.messageRows(ctx, messageQuery, selector)
	if err != nil {
		return err
	}
	for _, row := range messages {
		if row != nil {
			mark(row.ConversationId)
		}
	}
	hasQueue, err := d.hasTable(ctx, "turn_queue")
	if err != nil {
		return err
	}
	if hasQueue {
		queueQuery := &legacyqueue.QueueRowsInput{}
		queueQuery.SetConversationIds(ids)
		value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: queueReaderTarget, Input: queueQuery})
		if err != nil {
			return err
		}
		queueOutput, ok := value.(*legacyqueue.QueueRowsOutput)
		if !ok || queueOutput == nil {
			return fmt.Errorf("turn queue reader returned %T", value)
		}
		for _, row := range queueOutput.Data {
			if row != nil {
				mark(row.ConversationId)
			}
		}
	}
	runEvidence, err := d.CollectRunEvidence(ctx, graph)
	if err != nil {
		return err
	}
	for _, row := range runEvidence.Current {
		if row != nil && row.ConversationId != nil {
			mark(*row.ConversationId)
		}
	}
	for _, row := range runEvidence.Legacy {
		if row != nil && row.ConversationId != nil {
			mark(*row.ConversationId)
		}
	}
	goals, err := d.goalRows(ctx, ids)
	if err != nil {
		return err
	}
	for _, row := range goals {
		if row != nil {
			mark(row.ConversationId)
		}
	}
	hasSchedules, err := d.hasTable(ctx, "schedule")
	if err != nil {
		return err
	}
	if hasSchedules {
		query := &schedread.ScheduleInput{}
		query.SetConversationIds(ids)
		rows, err := d.scheduleRows(ctx, query, strings.TrimSpace(d.OwnerID(ctx)))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row != nil && row.ConversationId != nil {
				mark(*row.ConversationId)
			}
		}
	}
	if present, err := d.hasTable(ctx, "report_run"); err != nil {
		return err
	} else if present {
		query := &reportrun.Input{}
		query.SetConversationIDs(ids)
		rows, err := d.reportRuns(ctx, query, strings.TrimSpace(d.OwnerID(ctx)))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row != nil {
				mark(row.ConversationId)
			}
		}
	}
	if present, err := d.hasTable(ctx, "report_export_job"); err != nil {
		return err
	} else if present {
		query := &jobread.Input{}
		query.SetConversationIDs(ids)
		rows, err := d.exportJobs(ctx, query, strings.TrimSpace(d.OwnerID(ctx)))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row != nil {
				mark(row.ConversationId)
			}
		}
	}
	if present, err := d.hasTable(ctx, "conversation_report_context"); err != nil {
		return err
	} else if present {
		query := &contextread.Input{}
		query.SetConversationIDs(ids)
		rows, err := d.reportContexts(ctx, query, strings.TrimSpace(d.OwnerID(ctx)))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row != nil {
				mark(row.ConversationId)
			}
		}
	}
	for _, id := range ids {
		node := graph.Nodes[id]
		if node == nil {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(node.Status))
		if status == "" || terminalConversationStatus(status) || knownActiveConversationStatus(status) {
			continue
		}
		if activity[id] {
			return fmt.Errorf("%w: conversation=%s status=%s", ErrNonTerminal, id, strings.TrimSpace(node.Status))
		}
	}
	return nil
}

func terminalConversationStatus(status string) bool {
	switch status {
	case "succeeded", "completed", "complete", "success", "done", "ok", "failed", "error", "canceled", "cancelled", "terminated", "compacted", "pruned":
		return true
	}
	return false
}

func knownActiveConversationStatus(status string) bool {
	switch status {
	case "running", "in_progress", "processing", "queued", "pending", "thinking", "streaming", "waiting_for_user", "prechecking", "executing", "open":
		return true
	}
	return false
}
