package conversationtree

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	legacyread "github.com/viant/agently-core/internal/datly/legacyrun/read"
	modelread "github.com/viant/agently-core/internal/datly/modelcall/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	toolread "github.com/viant/agently-core/internal/datly/toolcall/read"
	turnread "github.com/viant/agently-core/internal/datly/turn/read"
	convturn "github.com/viant/agently-core/internal/store/conversation"

	"github.com/viant/agently-core/internal/datly/dbtime"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

var ErrConversationActive = errors.New("conversation is still in progress")

type RunEvidence struct {
	Current []*runread.RunRowsView
	Legacy  []*legacyread.LegacyRun
	// Includes dangling references; deleting the referenced run must not race
	// another writer merely because no matching row was returned by the reader.
	ReferencedIDs []string
}

var modelReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[modelread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/model-call"},
}
var toolReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[toolread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-call"},
}
var legacyReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[legacyread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/scheduler/legacy-run"},
}

// CollectRunEvidence follows all current-run links used by the legacy graph
// policy and reads legacy schedule-run rows. It requires an authorized graph.
func (d *Discoverer) CollectRunEvidence(ctx context.Context, graph *Graph) (*RunEvidence, error) {
	return d.collectRunEvidence(ctx, graph, nil)
}

func (d *Discoverer) collectRunEvidence(ctx context.Context, graph *Graph, turns []*turnread.TurnRowsView) (*RunEvidence, error) {
	if d == nil || d.Invoker == nil || d.OwnerID == nil {
		return nil, fmt.Errorf("conversation graph reader is not configured")
	}
	if err := d.authorize(ctx, graph); err != nil {
		return nil, err
	}
	evidence := &RunEvidence{}
	if len(graph.Nodes) == 0 {
		return evidence, nil
	}
	conversationIDs := make([]string, 0, len(graph.Nodes))
	legacyIDs := make([]string, 0)
	for id, node := range graph.Nodes {
		conversationIDs = append(conversationIDs, id)
		if node != nil && node.ScheduleRunID != "" {
			legacyIDs = append(legacyIDs, node.ScheduleRunID)
		}
	}
	conversationIDs, legacyIDs = normalizeIDs(conversationIDs), normalizeIDs(legacyIDs)
	turnRows := turns
	if turnRows == nil {
		turnInput := &turnread.TurnRowsInput{}
		turnInput.SetConversationIDs(conversationIDs)
		var err error
		turnRows, err = (&convturn.TurnStore{Invoker: d.Invoker}).ListRows(ctx, turnInput, deleteSelectors("id", "run_id"))
		if err != nil {
			return nil, err
		}
	}
	turnIDs, explicitRunIDs := []string{}, []string{}
	for _, row := range turnRows {
		if row == nil {
			continue
		}
		turnIDs = append(turnIDs, row.Id)
		if row.RunId != nil {
			explicitRunIDs = append(explicitRunIDs, *row.RunId)
		}
	}
	turnIDs = normalizeIDs(turnIDs)
	if len(turnIDs) > 0 {
		modelRuns, err := d.callRunIDs(ctx, turnIDs, true)
		if err != nil {
			return nil, err
		}
		toolRuns, err := d.callRunIDs(ctx, turnIDs, false)
		if err != nil {
			return nil, err
		}
		explicitRunIDs = append(explicitRunIDs, modelRuns...)
		explicitRunIDs = append(explicitRunIDs, toolRuns...)
	}
	explicitRunIDs = normalizeIDs(explicitRunIDs)
	evidence.ReferencedIDs = explicitRunIDs
	currentByID := map[string]*runread.RunRowsView{}
	byConversation := &runread.RunRowsInput{}
	byConversation.SetConversationIds(conversationIDs)
	runQueries := []*runread.RunRowsInput{byConversation}
	if len(turnIDs) > 0 {
		byTurn := &runread.RunRowsInput{}
		byTurn.SetTurnIds(turnIDs)
		runQueries = append(runQueries, byTurn)
	}
	if len(explicitRunIDs) > 0 {
		byID := &runread.RunRowsInput{}
		byID.SetIds(explicitRunIDs)
		runQueries = append(runQueries, byID)
	}
	for _, query := range runQueries {
		rows, err := ReadCleanupRuns(ctx, d.Invoker, query, false)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row != nil {
				currentByID[row.Id] = row
			}
		}
	}
	for _, id := range sortedMapKeys(currentByID) {
		evidence.Current = append(evidence.Current, currentByID[id])
	}
	hasLegacyRuns, err := d.hasTable(ctx, "schedule_run")
	if err != nil {
		return nil, err
	}
	if !hasLegacyRuns {
		return evidence, nil
	}
	legacyByID := map[string]*legacyread.LegacyRun{}
	legacyByConversation := &legacyread.Input{}
	legacyByConversation.SetConversationIDs(conversationIDs)
	rows, err := d.legacyRuns(ctx, legacyByConversation)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row != nil {
			legacyByID[row.Id] = row
		}
	}
	if len(legacyIDs) > 0 {
		legacyByIDInput := &legacyread.Input{}
		legacyByIDInput.SetIDs(legacyIDs)
		rows, err = d.legacyRuns(ctx, legacyByIDInput)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row != nil {
				legacyByID[row.Id] = row
			}
		}
	}
	for _, id := range sortedMapKeys(legacyByID) {
		evidence.Legacy = append(evidence.Legacy, legacyByID[id])
	}
	return evidence, nil
}

func (d *Discoverer) callRunIDs(ctx context.Context, turnIDs []string, model bool) ([]string, error) {
	owner := strings.TrimSpace(d.OwnerID(ctx))
	kind := "toolcallaccess"
	if model {
		kind = "modelcallaccess"
	}
	providers := []locator.Provider{
		provider.Named(kind, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "internal":
				return true, true, nil
			case "mode":
				return "rows", true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
		queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: []string{"message_id", "turn_id", "run_id"}}}}),
	}
	result := []string{}
	if model {
		input := &modelread.ModelCallsInput{}
		input.SetTurnIds(turnIDs)
		value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: modelReaderTarget, Input: input, Providers: providers})
		if err != nil {
			return nil, err
		}
		out, ok := value.(*modelread.ModelCallsOutput)
		if !ok || out == nil {
			return nil, fmt.Errorf("model call reader returned %T", value)
		}
		for _, row := range out.Data {
			if row != nil && row.RunId != nil {
				result = append(result, *row.RunId)
			}
		}
	} else {
		input := &toolread.ToolCallsInput{}
		input.SetTurnIds(turnIDs)
		value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: toolReaderTarget, Input: input, Providers: providers})
		if err != nil {
			return nil, err
		}
		out, ok := value.(*toolread.ToolCallsOutput)
		if !ok || out == nil {
			return nil, fmt.Errorf("tool call reader returned %T", value)
		}
		for _, row := range out.Data {
			if row != nil && row.RunId != nil {
				result = append(result, *row.RunId)
			}
		}
	}
	return normalizeIDs(result), nil
}

func (d *Discoverer) legacyRuns(ctx context.Context, input *legacyread.Input) ([]*legacyread.LegacyRun, error) {
	providers := []locator.Provider{provider.Named("schedulerunaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "internal" {
			return true, true, nil
		}
		return nil, false, nil
	})}
	providers = append(providers, queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: LegacyRunEvidenceFields()}}}))
	value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: legacyReaderTarget, Input: input, Providers: providers})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*legacyread.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("legacy schedule run reader returned %T", value)
	}
	return out.Data, nil
}

func (e *RunEvidence) Validate(now time.Time) error {
	if e == nil {
		return nil
	}
	now = now.UTC()
	for _, row := range e.Current {
		if row == nil || !activeRunStatus(row.Status) {
			continue
		}
		lease, invalid := evidenceTime(row.LeaseUntilRaw, row.LeaseUntil)
		if invalid || lease != nil && lease.After(now) {
			return ErrConversationActive
		}
		interval := 5
		if row.HeartbeatIntervalSec != nil {
			interval = *row.HeartbeatIntervalSec
		}
		if interval < 0 {
			interval = 0
		}
		grace := 2 * time.Duration(interval) * time.Second
		if grace < 15*time.Second {
			grace = 15 * time.Second
		}
		heartbeat, _ := evidenceTime(row.HeartbeatRaw, row.LastHeartbeatAt)
		if heartbeat != nil && !heartbeat.Before(now.Add(-grace)) {
			return ErrConversationActive
		}
	}
	for _, row := range e.Legacy {
		if row != nil && activeRunStatus(row.Status) {
			lease, invalid := evidenceTime(row.LeaseUntilRaw, row.LeaseUntil)
			if invalid || lease != nil && lease.After(now) {
				return ErrConversationActive
			}
		}
	}
	return nil
}

func activeRunStatus(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pending", "prechecking", "queued", "running":
		return true
	}
	return false
}

func sortedMapKeys[T any](rows map[string]T) []string {
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// CollectInitialRunIDs preserves raw current-run links before deletion
// preparation adds wakeup schedules. A dangling turn/call link still counts
// for the system scheduled-conversation fallback policy.
func (d *Discoverer) CollectInitialRunIDs(ctx context.Context, graph *Graph) ([]string, error) {
	if d == nil || d.Invoker == nil || d.OwnerID == nil {
		return nil, fmt.Errorf("conversation graph reader is not configured")
	}
	if err := d.authorize(ctx, graph); err != nil {
		return nil, err
	}
	if len(graph.Nodes) == 0 {
		return nil, nil
	}
	ids := []string{}
	conversationIDs := sortedMapKeys(graph.Nodes)
	query := &turnread.TurnRowsInput{}
	query.SetConversationIDs(conversationIDs)
	turns, err := (&convturn.TurnStore{Invoker: d.Invoker}).ListRows(ctx, query, state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: []string{"id", "run_id"}}}})
	if err != nil {
		return nil, err
	}
	turnIDs := []string{}
	for _, row := range turns {
		if row != nil {
			turnIDs = append(turnIDs, row.Id)
			if row.RunId != nil {
				ids = append(ids, *row.RunId)
			}
		}
	}
	turnIDs = normalizeIDs(turnIDs)
	runQueries := []*runread.RunRowsInput{}
	byConversation := &runread.RunRowsInput{}
	byConversation.SetConversationIds(conversationIDs)
	runQueries = append(runQueries, byConversation)
	if len(turnIDs) > 0 {
		byTurn := &runread.RunRowsInput{}
		byTurn.SetTurnIds(turnIDs)
		runQueries = append(runQueries, byTurn)
		for _, model := range []bool{true, false} {
			references, err := d.callRunIDs(ctx, turnIDs, model)
			if err != nil {
				return nil, err
			}
			ids = append(ids, references...)
		}
	}
	for _, query := range runQueries {
		rows, err := ReadCleanupRuns(ctx, d.Invoker, query, false)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row != nil {
				ids = append(ids, row.Id)
			}
		}
	}
	return normalizeIDs(ids), nil
}

// RunEvidenceFields avoids decoding malformed lease/heartbeat timestamps and
// keeps the raw-text classification identical to historical deletion policy.
func RunEvidenceFields() []string {
	return []string{"id", "status", "conversation_id", "turn_id", "schedule_id", "resumed_from_run_id", "lease_until_raw", "heartbeat_raw", "heartbeat_interval_sec", "activity_raw"}
}
func LegacyRunEvidenceFields() []string {
	return []string{"id", "status", "schedule_id", "conversation_id", "lease_until_raw", "activity_raw"}
}
func evidenceTime(raw *string, typed *time.Time) (*time.Time, bool) {
	if raw == nil {
		return typed, false
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil, false
	}
	parsed, ok := dbtime.ParseActivity(value)
	if !ok {
		return nil, true
	}
	return &parsed, false
}
