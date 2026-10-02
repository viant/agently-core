package write

import (
	"context"
	"embed"
	"fmt"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	native "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/tag"
	xdatly "github.com/viant/xdatly"
	xhandler "github.com/viant/xdatly/handler"
	"reflect"
	"strings"
	"time"
)

// CoreWriterComponent keeps the original per-turn business allocation around the canonical native writer.
type CoreWriterComponent struct {
	Contract xdatly.Component[Input, Output] `component:"CoreWrite,path=/v1/internal/agently/message/write,method=PATCH,connector=agently,view=writer,handler=CoreWrite,internal=true" caseFormat:"lc"`
}

func (CoreWriterComponent) EmbedFS() *embed.FS     { return &WriterDatlyResources }
func (CoreWriterComponent) EmbedNamespace() string { return WriterDatlyResourceNamespace }
func (CoreWriterComponent) DatlyHandler(name string) func() (rh.TypedHandler, error) {
	if name != "CoreWrite" {
		return nil
	}
	return func() (rh.TypedHandler, error) {
		typ := reflect.TypeFor[WriterComponent]()
		field, _ := typ.FieldByName("Contract")
		metadata, _, err := tag.ParseComponent(field.Tag)
		if err != nil {
			return nil, err
		}
		canonical, err := (&bootstrap.RouteSource{PackagePath: typ.PkgPath(), HolderType: typ.Name(), FieldName: field.Name, Tag: metadata, InputType: "Input", OutputType: "Output"}).Resolve(reflect.TypeFor[Input](), reflect.TypeFor[Output]())
		if err != nil {
			return nil, err
		}
		inner, err := native.New(canonical, reflect.TypeFor[Input](), reflect.TypeFor[Output](), "patch")
		if err != nil {
			return nil, err
		}
		return &coreWriter{Handler: inner}, nil
	}
}

type allocationSnapshot struct {
	native    any
	allocated map[string]*Message
	inserted  map[string]bool
	originals []*Message
}
type allocationContext struct{}
type insertReplayContext struct{}
type replayState struct {
	ids       map[string]bool
	attempt   int
	originals []*Message
}
type coreWriter struct{ *native.Handler }

func (h *coreWriter) CaptureInput(ctx context.Context, input any) (any, error) {
	native, err := h.Handler.CaptureInput(ctx, input)
	if err != nil {
		return nil, err
	}
	inputRows := input.(*Input).Messages
	if replay, ok := ctx.Value(insertReplayContext{}).(*replayState); ok {
		inputRows = replay.originals
	}
	return &allocationSnapshot{native: native, allocated: map[string]*Message{}, inserted: map[string]bool{}, originals: append([]*Message(nil), inputRows...)}, nil
}
func unwrap(i rh.Invocation) rh.Invocation {
	if s, ok := i.Snapshot.(*allocationSnapshot); ok {
		i.Snapshot = s.native
	}
	return i
}
func (h *coreWriter) Execute(ctx context.Context, i rh.Invocation) (any, error) {
	s, ok := i.Snapshot.(*allocationSnapshot)
	if !ok {
		return nil, fmt.Errorf("missing message allocation snapshot")
	}
	return h.Handler.Execute(context.WithValue(ctx, allocationContext{}, s), unwrap(i))
}
func (h *coreWriter) FinalizeOutcome(ctx context.Context, i rh.Invocation, result any, outcome xhandler.Outcome) error {
	if err := h.Handler.FinalizeOutcome(ctx, unwrap(i), result, outcome); err != nil {
		return err
	}
	if _, replay := ctx.Value(insertReplayContext{}).(*replayState); !replay {
		return nil
	}
	if outcome.Error != nil || len(outcome.Transactions) != 1 || outcome.Transactions[0].State != xhandler.TransactionCommitted {
		return nil
	}
	snapshot, ok := i.Snapshot.(*allocationSnapshot)
	if !ok {
		return nil
	}
	output, ok := result.(*Output)
	if !ok || output == nil {
		return nil
	}
	originals := make(map[string][]*Message, len(snapshot.originals))
	for _, row := range snapshot.originals {
		if row != nil {
			originals[row.Id] = append(originals[row.Id], row)
		}
	}
	resolved := make([]*Message, len(output.Data))
	for index, row := range output.Data {
		if row == nil {
			continue
		}
		queue := originals[row.Id]
		if len(queue) == 0 {
			return fmt.Errorf("message result has unexpected identity %q", row.Id)
		}
		originals[row.Id] = queue[1:]
		resolved[index] = queue[0]
	}
	for _, queue := range originals {
		if len(queue) != 0 {
			return fmt.Errorf("message result omitted input occurrences")
		}
	}
	for index, row := range output.Data {
		if row != nil && resolved[index] != row {
			*resolved[index] = *row
		}
	}
	output.Data = resolved
	return nil
}
func (*coreWriter) SupportsMutationRecovery() bool { return true }
func (*coreWriter) ScopedMutationRetryLimit() int  { return 9 }
func (*coreWriter) RecoverScopedMutation(ctx context.Context, i rh.Invocation, report dexec.MutationReport, outcome xhandler.Outcome) (bool, error) {
	s, ok := i.Snapshot.(*allocationSnapshot)
	if !ok || len(s.allocated) == 0 || report.Nested || len(outcome.Transactions) != 1 || outcome.Transactions[0].State != xhandler.TransactionRolledBack {
		return false, nil
	}
	collision := false
	for _, result := range report.Results {
		if result.Error == nil {
			continue
		}
		text := strings.ToLower(result.Error.Error())
		unique := (strings.Contains(text, "unique constraint failed") && strings.Contains(text, "message.turn_id") && strings.Contains(text, "message.sequence")) || ((strings.Contains(text, "duplicate entry") || strings.Contains(text, "duplicate key value violates unique constraint")) && strings.Contains(text, "idx_message_turn_seq"))
		if !unique {
			return false, nil
		}
		collision = true
	}

	if collision {
		var deps struct {
			Invoker dexec.ComponentInvoker `bind:"kind=component_invoker,required"`
		}
		if err := i.Binder.Bind(ctx, &deps); err != nil {
			return false, err
		}
		matched := false
		input, _ := i.Input.(*Input)
		for _, row := range input.Messages {
			if row.Sequence == nil || !nonempty(row.TurnId) {
				continue
			}
			exists, err := sequenceCollision(ctx, deps.Invoker, row)
			if err != nil {
				return false, err
			}
			if !exists {
				continue
			}
			if s.allocated[row.Id] == nil {
				return false, nil
			}
			matched = true
		}
		if matched {
			attempt := 0
			if replay, ok := ctx.Value(insertReplayContext{}).(*replayState); ok {
				attempt = replay.attempt
			}
			timer := time.NewTimer(time.Duration(attempt+1) * 5 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-timer.C:
			}
			if attempt >= 9 {
				return false, nil
			}
		}
		return matched, nil
	}
	return false, nil
}
func (*coreWriter) MutationReplayContext(ctx context.Context, i rh.Invocation) context.Context {
	ids := map[string]bool{}
	attempt := 1
	var originals []*Message
	if prior, ok := ctx.Value(insertReplayContext{}).(*replayState); ok {
		attempt = prior.attempt + 1
		originals = prior.originals
		for id := range prior.ids {
			ids[id] = true
		}
	}
	if s, ok := i.Snapshot.(*allocationSnapshot); ok {
		if originals == nil {
			originals = s.originals
		}
		for id := range s.inserted {
			ids[id] = true
		}
	}
	return context.WithValue(ctx, insertReplayContext{}, &replayState{ids: ids, attempt: attempt, originals: originals})
}
