package scheduledelete

import (
	"context"
	"fmt"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	"reflect"
	"sort"
	"strings"
	"time"

	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	legacyread "github.com/viant/agently-core/internal/datly/legacyrun/read"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	schedbase "github.com/viant/agently-core/internal/datly/schedule/base"
	schedread "github.com/viant/agently-core/internal/datly/schedule/read"
	conversation "github.com/viant/agently-core/internal/store/conversation"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

var scheduleTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[schedbase.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/schedule/base"}}
var runTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[runread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"}}
var legacyTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[legacyread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/scheduler/legacy-run"}}

func trustedProviders(kind, owner string) []locator.Provider {
	return []locator.Provider{provider.Named(kind, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "internal":
			return true, true, nil
		case "mode":
			return "rows", true, nil
		}
		return nil, false, nil
	}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil })}
}

func readSchedule(ctx context.Context, invoker dexec.ComponentInvoker, id, owner string) (*schedbase.ScheduleBaseView, error) {
	input := &schedread.ScheduleInput{}
	input.SetId(id)
	providers := trustedProviders("scheduleaccess", owner)
	options := queryselectors.ForUpdateOptions(ctx, true)
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: options, Target: scheduleTarget, Input: input, Providers: providers})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*schedbase.ScheduleOutput)
	if !ok || output == nil {
		return nil, fmt.Errorf("schedule reader returned %T", value)
	}
	if len(output.Data) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrScheduleNotFound, id)
	}
	if len(output.Data) != 1 || output.Data[0] == nil {
		return nil, fmt.Errorf("schedule identity returned %d rows", len(output.Data))
	}
	return output.Data[0], nil
}

func readRuns(ctx context.Context, invoker dexec.ComponentInvoker, input *runread.RunRowsInput, owner string, lock bool) ([]*runread.RunRowsView, error) {
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: queryselectors.ForUpdateOptions(ctx, lock), Target: runTarget, Input: input, Providers: trustedProviders("runaccess", owner)})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*runread.RunRowsOutput)
	if !ok || output == nil {
		return nil, fmt.Errorf("run reader returned %T", value)
	}
	return output.Data, nil
}
func readLegacy(ctx context.Context, invoker dexec.ComponentInvoker, input *legacyread.Input, owner string, lock bool) ([]*legacyread.LegacyRun, error) {
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: queryselectors.ForUpdateOptions(ctx, lock), Target: legacyTarget, Input: input, Providers: trustedProviders("schedulerunaccess", owner)})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*legacyread.Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("legacy run reader returned %T", value)
	}
	return output.Data, nil
}
func legacyPresent(ctx context.Context, schema tree.TableInspector) (bool, error) {
	if schema == nil {
		return false, fmt.Errorf("schedule deletion requires linked schema metadata")
	}
	return schema.HasTable(ctx, "agently", "schedule_run")
}
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
func normalize(ids []string) []string {
	values := map[string]bool{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			values[id] = true
		}
	}
	result := make([]string, 0, len(values))
	for id := range values {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

// ConversationRoots follows stored scheduler links, loads their actual rows,
// and removes candidates whose direct parent is also selected. Missing linked
// conversations are intentionally ignored, matching legacy orphan handling.
func ConversationRoots(ctx context.Context, invoker dexec.ComponentInvoker, owner string, ids []string, query *convread.ConversationInput) ([]string, error) {
	store := &conversation.Store{Invoker: invoker, OwnerID: func(context.Context) string { return owner }}
	byID := map[string]*convread.ConversationView{}
	if query != nil {
		rows, err := store.GraphRows(ctx, query)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row != nil {
				byID[row.Id] = row
			}
		}
	}
	if ids = normalize(ids); len(ids) > 0 {
		input := &convread.ConversationInput{}
		input.SetIds(ids)
		rows, err := store.GraphRows(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row != nil {
				byID[row.Id] = row
			}
		}
	}
	rows := make([]*convread.ConversationView, 0, len(byID))
	for _, row := range byID {
		if byID[deref(row.ConversationParentId)] == nil {
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].Id < rows[j].Id
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.Id)
	}
	return result, nil
}

// scheduleLeaseActive retains the legacy conservative interpretation of raw DB
// timestamps. Equal expiry is expired; malformed nonempty values remain live.
func scheduleLeaseActive(value *string, now time.Time) bool {
	if value == nil || strings.TrimSpace(*value) == "" {
		return false
	}
	raw := strings.TrimSpace(*value)
	if strings.EqualFold(raw, "0000-00-00 00:00:00") {
		return true
	}
	layouts := []string{time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05-07:00", "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05",
		"2006-01-02 15:04:05 -0700 MST", "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC().After(now)
		}
		if parsed, err := time.ParseInLocation(layout, raw, time.UTC); err == nil {
			return parsed.UTC().After(now)
		}
	}
	return true
}
