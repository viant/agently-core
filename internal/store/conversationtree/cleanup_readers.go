package conversationtree

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	conversationmeta "github.com/viant/agently-core/internal/datly/conversation/cleanup/read"
	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	runmeta "github.com/viant/agently-core/internal/datly/run/cleanup/read"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	"github.com/viant/agently-core/internal/store/maintenancebatch"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

var cleanupConversationTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[conversationmeta.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/conversation/cleanup"},
}
var cleanupRunTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[runmeta.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/run/cleanup"},
}

// These adapters are private deletion metadata reads, never replacements for
// public/list/candidate readers. Every invocation has exactly one bounded
// predicate, at most 400 binds, and fresh execution (including after locks).
func ReadCleanupConversations(ctx context.Context, invoker dexec.ComponentInvoker, input *convread.ConversationInput, lock bool) ([]*convread.ConversationView, error) {
	if input == nil || invoker == nil {
		return nil, fmt.Errorf("cleanup conversation reader is not configured")
	}
	key, ids, err := cleanupFilter(input.Has, map[string][]string{
		"Id": {input.Id}, "Ids": input.Ids, "ParentId": {input.ParentId}, "ParentIds": input.ParentIds,
		"ParentTurnId": {input.ParentTurnId}, "ParentTurnIds": input.ParentTurnIds,
		"ScheduleId": {input.ScheduleId}, "ScheduleRunId": {input.ScheduleRunId},
	})
	if err != nil {
		return nil, err
	}
	var rows []*convread.ConversationView
	err = cleanupReadBatches(ctx, ids, func(ids []string) error {
		query := &conversationmeta.Input{}
		switch key {
		case "Id", "Ids":
			query.SetIDs(ids)
		case "ParentId", "ParentIds":
			query.SetParentIDs(ids)
		case "ParentTurnId", "ParentTurnIds":
			query.SetParentTurnIDs(ids)
		case "ScheduleId":
			query.SetScheduleIDs(ids)
		case "ScheduleRunId":
			query.SetScheduleRunIDs(ids)
		}
		value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: cleanupConversationTarget, Input: query,
			Providers: cleanupScope("conversationcleanupscope"), ReaderOptions: queryselectors.ForUpdateOptions(ctx, lock)})
		if err != nil {
			return err
		}
		out, ok := value.(*conversationmeta.Output)
		if !ok || out == nil {
			return fmt.Errorf("cleanup conversation reader returned %T", value)
		}
		for _, row := range out.Data {
			if row != nil {
				rows = append(rows, &convread.ConversationView{Id: row.Id, ConversationParentId: row.ConversationParentId,
					ConversationParentTurnId: row.ConversationParentTurnId, Scheduled: row.Scheduled, ScheduleId: row.ScheduleId,
					ScheduleRunId: row.ScheduleRunId, ScheduleKind: row.ScheduleKind, CreatedAtRaw: row.CreatedAtRaw, ActivityRaw: row.ActivityRaw})
			}
		}
		return nil
	})
	return rows, err
}

func ReadCleanupRuns(ctx context.Context, invoker dexec.ComponentInvoker, input *runread.RunRowsInput, lock bool) ([]*runread.RunRowsView, error) {
	if input == nil || invoker == nil {
		return nil, fmt.Errorf("cleanup run reader is not configured")
	}
	key, ids, err := cleanupFilter(input.Has, map[string][]string{
		"Id": {input.Id}, "Ids": input.Ids, "ConversationId": {input.ConversationId}, "ConversationIds": input.ConversationIds,
		"TurnId": {input.TurnId}, "TurnIds": input.TurnIds, "ScheduleId": {input.ScheduleId}, "ScheduleIds": input.ScheduleIds,
		"ResumedFromRunIds": input.ResumedFromRunIds,
	})
	if err != nil {
		return nil, err
	}
	var rows []*runread.RunRowsView
	err = cleanupReadBatches(ctx, ids, func(ids []string) error {
		query := &runmeta.Input{}
		switch key {
		case "Id", "Ids":
			query.SetIDs(ids)
		case "ConversationId", "ConversationIds":
			query.SetConversationIDs(ids)
		case "TurnId", "TurnIds":
			query.SetTurnIDs(ids)
		case "ScheduleId", "ScheduleIds":
			query.SetScheduleIDs(ids)
		case "ResumedFromRunIds":
			query.SetResumedFromRunIDs(ids)
		}
		value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: cleanupRunTarget, Input: query,
			Providers: cleanupScope("runcleanupscope"), ReaderOptions: queryselectors.ForUpdateOptions(ctx, lock)})
		if err != nil {
			return err
		}
		out, ok := value.(*runmeta.Output)
		if !ok || out == nil {
			return fmt.Errorf("cleanup run reader returned %T", value)
		}
		for _, row := range out.Data {
			if row != nil {
				rows = append(rows, &runread.RunRowsView{Id: row.Id, Status: row.Status, ConversationId: row.ConversationId,
					TurnId: row.TurnId, ScheduleId: row.ScheduleId, ResumedFromRunId: row.ResumedFromRunId,
					ConversationKind: row.ConversationKind, LeaseUntilRaw: row.LeaseUntilRaw, HeartbeatRaw: row.HeartbeatRaw,
					HeartbeatIntervalSec: row.HeartbeatIntervalSec, ActivityRaw: row.ActivityRaw})
			}
		}
		return nil
	})
	return rows, err
}

func cleanupScope(kind string) []locator.Provider {
	return []locator.Provider{provider.Named(kind, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "internal" {
			return true, true, nil
		}
		return nil, false, nil
	})}
}

// Reject unsupported flags instead of silently broadening a canonical request.
func cleanupFilter(marker any, allowed map[string][]string) (string, []string, error) {
	value := reflect.ValueOf(marker)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return "", nil, fmt.Errorf("cleanup metadata read requires a bounded predicate")
	}
	value = value.Elem()
	key := ""
	for i := 0; i < value.NumField(); i++ {
		if !value.Field(i).Bool() {
			continue
		}
		name := value.Type().Field(i).Name
		if _, ok := allowed[name]; !ok || key != "" {
			return "", nil, fmt.Errorf("cleanup metadata read requires exactly one supported predicate")
		}
		key = name
	}
	if key == "" || len(allowed[key]) == 0 {
		return "", nil, fmt.Errorf("cleanup metadata predicate requires IDs")
	}
	for _, id := range allowed[key] {
		if strings.TrimSpace(id) == "" {
			return "", nil, fmt.Errorf("cleanup metadata identity is empty")
		}
	}
	return key, normalizeIDs(allowed[key]), nil
}

func cleanupReadBatches(ctx context.Context, ids []string, read func([]string) error) error {
	for start := 0; start < len(ids); start += maintenancebatch.Size {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := start + maintenancebatch.Size
		if end > len(ids) {
			end = len(ids)
		}
		if err := read(ids[start:end]); err != nil {
			return err
		}
	}
	return nil
}
