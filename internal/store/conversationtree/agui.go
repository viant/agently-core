package conversationtree

import (
	"context"
	"fmt"
	agui "github.com/viant/agently-core/app/store/agui"
	eventdelete "github.com/viant/agently-core/internal/datly/agui/cleanup/event/delete"
	eventread "github.com/viant/agently-core/internal/datly/agui/cleanup/event/read"
	rundelete "github.com/viant/agently-core/internal/datly/agui/cleanup/run/delete"
	runread "github.com/viant/agently-core/internal/datly/agui/cleanup/run/read"
	"github.com/viant/agently-core/internal/store/maintenancebatch"
	"github.com/viant/agently-core/internal/store/maintenancetx"
	"github.com/viant/bindly/locator"
	"github.com/viant/datly/runtime/handler/provider"
	"reflect"
	"sort"
	"time"
)

func aguiCleanupProviders() []locator.Provider {
	return []locator.Provider{provider.Named("conversationtreescope", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "internal" {
			return true, true, nil
		}
		return nil, false, nil
	})}
}
func aguiCleanupString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// Only native internal conversation/run identities establish deletion scope.
// Public protocol identifiers never enter the execution run deletion set.
func (d *Discoverer) collectAGUI(ctx context.Context, plan *DeletePlan, now time.Time) error {
	if len(plan.ConversationIDs) == 0 || !plan.Tables["run"] {
		return nil
	}
	if err := eachDeleteBatch(plan.ConversationIDs, func(ids []string) error {
		input := &runread.Input{}
		input.SetThreadIDs(ids)
		rows, err := planReaderRows[runread.Run](ctx, d, input, "/v1/internal/agently/ag-ui/cleanup/run", aguiCleanupProviders(), d.LockDetachRows)
		if err != nil {
			return err
		}
		plan.AGUIRuns = append(plan.AGUIRuns, rows...)
		if plan.Tables["call_payload"] {
			query := &eventread.Input{}
			query.SetThreadIDs(ids)
			events, err := planReaderRows[eventread.Event](ctx, d, query, "/v1/internal/agently/ag-ui/cleanup/event", aguiCleanupProviders())
			if err != nil {
				return err
			}
			plan.AGUIEvents = append(plan.AGUIEvents, events...)
		}
		return nil
	}); err != nil {
		return err
	}
	runs := map[string]*runread.Run{}
	for _, row := range plan.AGUIRuns {
		if row == nil || aguiCleanupString(row.RunKey) == "" || plan.Graph.Nodes[aguiCleanupString(row.ThreadId)] == nil {
			return fmt.Errorf("AG-UI run cleanup scope mismatch")
		}
		if _, exists := runs[*row.RunKey]; exists {
			return fmt.Errorf("duplicate AG-UI run cleanup identity")
		}
		runs[*row.RunKey] = row
		status := aguiCleanupString(row.Status)
		if !d.rootQualified && (status == agui.StatusAdmitted || status == agui.StatusRunning) && row.LeaseUntil != nil && row.LeaseUntil.After(now.UTC()) {
			return ErrConversationActive
		}
	}
	for _, event := range plan.AGUIEvents {
		if event == nil || aguiCleanupString(event.EventKey) == "" || runs[aguiCleanupString(event.RunKey)] == nil || aguiCleanupString(runs[*event.RunKey].ThreadId) != aguiCleanupString(event.ThreadId) {
			return fmt.Errorf("AG-UI payload cleanup scope mismatch")
		}
	}
	return nil
}

// Join the parent's existing managed transaction. Remove protocol journal
// payloads before protocol run rows. The tree owns conversation deletion;
// there is no separate physical thread/lease table or deletion phase.
func (m *Mutator) deleteAGUI(ctx context.Context, plan *DeletePlan) error {
	if err := m.deleteAGUIEvents(ctx, plan); err != nil {
		return err
	}
	return m.deleteAGUIRuns(ctx, plan)
}

func (m *Mutator) deleteAGUIEvents(ctx context.Context, plan *DeletePlan) error {
	if maintenancetx.Enabled(ctx) {
		return m.deleteMaintenanceAGUIEvents(ctx, plan)
	}
	return eachDeleteBatch(plan.ConversationIDs, func(scope []string) error {
		allowed := treeIDSet(scope)
		runKeys := []string{}
		for _, run := range plan.AGUIRuns {
			if run != nil && run.RunKey != nil && run.ThreadId != nil && allowed[*run.ThreadId] {
				runKeys = append(runKeys, *run.RunKey)
			}
		}
		ids := []string{}
		for _, row := range plan.AGUIEvents {
			if row == nil || row.EventKey == nil || row.ThreadId == nil {
				return fmt.Errorf("AG-UI payload cleanup identity unavailable")
			}
			if allowed[*row.ThreadId] {
				ids = append(ids, *row.EventKey)
			}
		}
		return treeDeleteIDs[eventdelete.EventDelete, eventdelete.Output](ctx, m, ids, "/v1/internal/agently/ag-ui/cleanup/event/delete", func(id string) *eventdelete.EventDelete {
			row := &eventdelete.EventDelete{}
			row.SetEventKey(id)
			row.SetShouldDelete(true)
			return row
		}, func(rows []*eventdelete.EventDelete) any {
			input := &eventdelete.Input{}
			input.SetRunKeys(runKeys)
			input.SetThreadIDs(scope)
			input.SetRows(rows)
			return input
		}, aguiCleanupProviders()...)
	})
}

func (m *Mutator) deleteAGUIRuns(ctx context.Context, plan *DeletePlan) error {
	return eachDeleteBatch(plan.ConversationIDs, func(scope []string) error {
		allowed := treeIDSet(scope)
		ids := []string{}
		for _, row := range plan.AGUIRuns {
			if row == nil || row.RunKey == nil || row.ThreadId == nil {
				return fmt.Errorf("AG-UI run cleanup identity unavailable")
			}
			if allowed[*row.ThreadId] {
				ids = append(ids, *row.RunKey)
			}
		}
		return treeDeleteIDs[rundelete.RunDelete, rundelete.Output](ctx, m, ids, "/v1/internal/agently/ag-ui/cleanup/run/delete", func(id string) *rundelete.RunDelete {
			row := &rundelete.RunDelete{}
			row.SetRunKey(id)
			row.SetShouldDelete(true)
			return row
		}, func(rows []*rundelete.RunDelete) any {
			input := &rundelete.Input{}
			input.SetDeleteThreadIDs(scope)
			input.SetThreadIDs(scope)
			input.SetRows(rows)
			return input
		}, aguiCleanupProviders()...)
	})
}

// Apply calls this before queuing any other mutations. Like a 3B event unit,
// it can execute scoped bulk SQL without a flush/anchor. Unlike a 3B unit it
// joins the original small-plan transaction: metadata failure restores events.
func (m *Mutator) deleteMaintenanceAGUIEvents(ctx context.Context, plan *DeletePlan) error {
	allowed := treeIDSet(plan.ConversationIDs)
	runs := map[string]string{}
	for _, row := range plan.AGUIRuns {
		if row == nil || aguiCleanupString(row.RunKey) == "" || !allowed[aguiCleanupString(row.ThreadId)] {
			return fmt.Errorf("AG-UI run cleanup scope mismatch")
		}
		runs[*row.RunKey] = *row.ThreadId
	}
	events := append([]*eventread.Event(nil), plan.AGUIEvents...)
	for _, row := range events {
		if row == nil || aguiCleanupString(row.EventKey) == "" || aguiCleanupString(row.RunKey) == "" ||
			!allowed[aguiCleanupString(row.ThreadId)] || runs[*row.RunKey] != *row.ThreadId {
			return fmt.Errorf("AG-UI payload cleanup scope mismatch")
		}
	}
	sort.Slice(events, func(i, j int) bool { return *events[i].EventKey < *events[j].EventKey })
	for start := 0; start < len(events); start += maintenancebatch.Size {
		if err := deleteMaintenanceEvents(ctx, m.SQL, events[start:min(start+maintenancebatch.Size, len(events))]); err != nil {
			return err
		}
	}
	return nil
}
