package conversationtree

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	goalread "github.com/viant/agently-core/internal/datly/goal/read"
	schedread "github.com/viant/agently-core/internal/datly/schedule/read"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

var ErrScheduleReferenced = errors.New("user schedule references the conversation graph")

var goalReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[goalread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/goal/{conversationId}"},
}
var scheduleReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[schedread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/scheduler/schedule/{id}"},
}

// ValidateScheduleReferences permits only owned internal goal-wakeup schedules
// with the exact legacy naming contract. The returned IDs are deletion inputs
// for the later managed transaction; this method does not mutate anything.
func (d *Discoverer) ValidateScheduleReferences(ctx context.Context, graph *Graph, now time.Time) ([]string, error) {
	if d == nil || d.Invoker == nil || d.OwnerID == nil {
		return nil, fmt.Errorf("conversation graph reader is not configured")
	}
	userID := strings.TrimSpace(d.OwnerID(ctx))
	if err := d.authorize(ctx, graph); err != nil {
		return nil, err
	}
	if len(graph.Nodes) == 0 {
		return nil, nil
	}
	conversationIDs := sortedMapKeys(graph.Nodes)
	goalIDs, err := d.goalIDs(ctx, conversationIDs)
	if err != nil {
		return nil, err
	}
	byID := map[string]*schedread.ScheduleView{}
	byConversation := &schedread.ScheduleInput{}
	byConversation.SetConversationIds(conversationIDs)
	rows, err := d.scheduleRows(ctx, byConversation, userID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row != nil {
			byID[row.Id] = row
		}
	}
	if len(goalIDs) > 0 {
		byGoal := &schedread.ScheduleInput{}
		byGoal.SetGoalIds(goalIDs)
		rows, err = d.scheduleRows(ctx, byGoal, userID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row != nil {
				byID[row.Id] = row
			}
		}
	}
	insideGoals := map[string]bool{}
	for _, id := range goalIDs {
		insideGoals[id] = true
	}
	allowed := make([]string, 0, len(byID))
	for _, id := range sortedMapKeys(byID) {
		row := byID[id]
		if !row.Internal {
			return nil, fmt.Errorf("%w: schedule=%s", ErrScheduleReferenced, id)
		}
		if owner := strings.TrimSpace(deref(row.CreatedByUserId)); !d.systemRetention && owner != "" && owner != userID {
			return nil, ErrPermissionDenied
		}
		conversationID := strings.TrimSpace(deref(row.ConversationId))
		goalID := strings.TrimSpace(deref(row.GoalId))
		if conversationID == "" || graph.Nodes[conversationID] == nil || goalID == "" || !insideGoals[goalID] ||
			!strings.EqualFold(strings.TrimSpace(row.ScheduleType), "adhoc") ||
			id != "goal-wakeup-"+goalID || strings.TrimSpace(row.Name) != "autonomous::goal-wakeup::"+goalID {
			return nil, fmt.Errorf("%w: internal schedule=%s is not an owned goal wakeup", ErrScheduleReferenced, id)
		}
		if row.LeaseUntil != nil && row.LeaseUntil.After(now.UTC()) {
			return nil, ErrConversationActive
		}
		allowed = append(allowed, id)
	}
	return allowed, nil
}

func (d *Discoverer) goalIDs(ctx context.Context, conversationIDs []string) ([]string, error) {
	rows, err := d.goalRows(ctx, conversationIDs)
	if err != nil {
		return nil, err
	}
	goalIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if row != nil {
			goalIDs = append(goalIDs, row.Id)
		}
	}
	return normalizeIDs(goalIDs), nil
}

func (d *Discoverer) goalRows(ctx context.Context, conversationIDs []string) ([]*goalread.GoalView, error) {
	if len(conversationIDs) == 0 {
		return nil, nil
	}
	present, err := d.hasTable(ctx, "goal")
	if err != nil || !present {
		return nil, err
	}
	goals := &goalread.GoalInput{}
	goals.SetConversationIDs(conversationIDs)
	goalProvider := provider.Named("goalaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "graph" {
			return true, true, nil
		}
		return nil, false, nil
	})
	value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: goalReaderTarget, Input: goals, Providers: []locator.Provider{goalProvider}})
	if err != nil {
		return nil, err
	}
	goalOutput, ok := value.(*goalread.GoalOutput)
	if !ok || goalOutput == nil {
		return nil, fmt.Errorf("goal reader returned %T", value)
	}
	return goalOutput.Data, nil
}

func (d *Discoverer) scheduleRows(ctx context.Context, input *schedread.ScheduleInput, owner string) ([]*schedread.ScheduleView, error) {
	providers := []locator.Provider{
		provider.Named("scheduleaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return true, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}
	value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: scheduleReaderTarget, Input: input, Providers: providers})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*schedread.ScheduleOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("schedule reader returned %T", value)
	}
	return out.Data, nil
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
