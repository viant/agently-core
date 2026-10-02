package data

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	convwrite "github.com/viant/agently-core/internal/datly/conversation/write"
	msgwrite "github.com/viant/agently-core/internal/datly/message/write"
	modelwrite "github.com/viant/agently-core/internal/datly/modelcall/write"
	payloadwrite "github.com/viant/agently-core/internal/datly/payload/write"
	toolwrite "github.com/viant/agently-core/internal/datly/toolcall/write"
	turnwrite "github.com/viant/agently-core/internal/datly/turn/write"
	queuewrite "github.com/viant/agently-core/internal/datly/turnqueue/write"
	toolcallmodel "github.com/viant/agently-core/model/toolcall"
	turnqueuemodel "github.com/viant/agently-core/model/turnqueue"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

func nativePatchData[T any](ctx context.Context, s *datlyService, kind string, rows []*T) ([]*T, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	switch kind {
	case "conversation":
		return invokeDataBatch[T, convwrite.MutableConversationView](ctx, s, &convwrite.Input{}, "Conversations", "/v1/api/agently/conversation", rows)
	case "message":
		return invokeDataBatch[T, msgwrite.Message](ctx, s, &msgwrite.Input{}, "Messages", "/v1/api/agently/message", rows)
	case "turn":
		return invokeDataBatch[T, turnwrite.Turn](ctx, s, &turnwrite.Input{}, "Turns", "/v1/api/agently/turn", rows)
	case "modelcall":
		return invokeDataBatch[T, modelwrite.ModelCall](ctx, s, &modelwrite.Input{}, "ModelCalls", "/v1/api/agently/modelcall", rows)
	case "toolcall":
		return invokeDataBatch[T, toolwrite.ToolCall](ctx, s, &toolwrite.Input{}, "ToolCalls", "/v1/api/agently/toolcall", rows)
	case "payload":
		return invokeDataBatch[T, payloadwrite.Payload](ctx, s, &payloadwrite.Input{}, "Payloads", "/v1/api/agently/payload", rows)
	}
	return nil, fmt.Errorf("unknown data mutation %q", kind)
}

// Every batch goes through one stock writer invocation so a late error rolls
// back preceding rows under the runtime's managed transaction policy.
func invokeDataBatch[T any, W any](ctx context.Context, s *datlyService, input any, field, path string, rows []*T) ([]*T, error) {
	mapped := make([]*W, 0, len(rows))
	for _, row := range rows {
		value, err := mapDataDTO[W](row)
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, value)
	}
	for _, row := range mapped {
		if call, ok := any(row).(*toolwrite.ToolCall); ok && call != nil && call.Has != nil && call.Has.ErrorMessage && call.ErrorMessage != nil {
			value := toolcallmodel.SanitizeErrorMessage(*call.ErrorMessage)
			call.ErrorMessage = &value
		}
	}

	value, err := s.invokeDataWriter(ctx, input, field, path, mapped)
	if err != nil {
		return nil, err
	}
	output := reflect.ValueOf(value)
	if output.Kind() != reflect.Pointer || output.IsNil() {
		return nil, fmt.Errorf("data writer returned %T", value)
	}
	data := output.Elem().FieldByName("Data")
	if !data.IsValid() {
		return nil, fmt.Errorf("data writer returned %T without rows", value)
	}

	// Stock writer output retains the prepared input row objects, even when
	// execution frames are reordered. Pointer queues preserve each occurrence.
	destinations := make(map[*W][]*T, len(rows))
	for i, native := range mapped {
		if rows[i] != nil {
			destinations[native] = append(destinations[native], rows[i])
		}
	}

	result := make([]*T, 0, data.Len())
	selected := make([]*T, 0, data.Len())
	for i := 0; i < data.Len(); i++ {
		native, ok := data.Index(i).Interface().(*W)
		if !ok {
			return nil, fmt.Errorf("data writer returned incompatible row %T", data.Index(i).Interface())
		}
		if native == nil {
			result = append(result, nil)
			selected = append(selected, nil)
			continue
		}

		queue := destinations[native]
		if len(queue) == 0 {
			return nil, fmt.Errorf("data writer returned a foreign mutation row")
		}
		destination := queue[0]
		destinations[native] = queue[1:]
		mapped, err := mapDataDTO[T](destination)
		if err != nil {
			return nil, err
		}
		if err = applyDataMutationResult(reflect.ValueOf(mapped).Elem(), reflect.ValueOf(native)); err != nil {
			return nil, err
		}
		result = append(result, mapped)
		selected = append(selected, destination)
	}
	for _, queue := range destinations {
		if len(queue) > 0 {
			return nil, fmt.Errorf("data writer omitted %d mutations", len(queue))
		}
	}
	// Duplicate identities stay in their input occurrence order; the stock writer
	// owns their update/conflict outcome and atomicity. Publish successful defaults
	// only after mapping the entire result, preserving public logical-only fields.
	for i := range result {
		if selected[i] == nil {
			continue
		}
		*selected[i] = *result[i]
		result[i] = selected[i]
	}

	return result, nil
}
func (s *datlyService) invokeDataWriter(ctx context.Context, input any, field, path string, rows any) (any, error) {
	if s == nil || s.native == nil {
		return nil, fmt.Errorf("native data runtime is required")
	}
	value := reflect.ValueOf(input).Elem()
	value.FieldByName(field).Set(reflect.ValueOf(rows))
	has := value.FieldByName("Has")
	has.Set(reflect.New(has.Type().Elem()))
	has.Elem().FieldByName(field).SetBool(true)
	target := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: value.Type().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: path}}
	if _, message := input.(*msgwrite.Input); message {
		target.Component.Scope = reflect.TypeFor[msgwrite.CoreWriterComponent]().PkgPath()
		target.Component.Name = "CoreWrite"
		target.Route.Path = "/v1/internal/agently/message/write"
	}
	return s.native.InvokeComponent(ctx, dexec.ComponentRequest{Target: target, Input: input})
}
func (s *datlyService) deleteDataNative(ctx context.Context, kind string, ids ...string) error {
	var input any
	var rows any
	var field, path string
	unique := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			unique = append(unique, id)
			seen[id] = true
		}
	}
	if len(unique) == 0 {
		return nil
	}
	switch kind {
	case "turn":
		changes := make([]*turnwrite.Turn, 0, len(unique))
		for _, id := range unique {
			row := &turnwrite.Turn{}
			row.SetId(id)
			row.SetShouldDelete(true)
			changes = append(changes, row)
		}
		input = &turnwrite.Input{}
		rows = changes
		field = "Turns"
		path = "/v1/api/agently/turn"
	case "modelcall":
		changes := make([]*modelwrite.ModelCall, 0, len(unique))
		for _, id := range unique {
			row := &modelwrite.ModelCall{}
			row.SetMessageId(id)
			row.SetShouldDelete(true)
			changes = append(changes, row)
		}
		input = &modelwrite.Input{}
		rows = changes
		field = "ModelCalls"
		path = "/v1/api/agently/modelcall"
	case "toolcall":
		changes := make([]*toolwrite.ToolCall, 0, len(unique))
		for _, id := range unique {
			row := &toolwrite.ToolCall{}
			row.SetMessageId(id)
			row.SetShouldDelete(true)
			changes = append(changes, row)
		}
		input = &toolwrite.Input{}
		rows = changes
		field = "ToolCalls"
		path = "/v1/api/agently/toolcall"
	case "payload":
		changes := make([]*payloadwrite.Payload, 0, len(unique))
		for _, id := range unique {
			row := &payloadwrite.Payload{}
			row.SetId(id)
			row.SetShouldDelete(true)
			changes = append(changes, row)
		}
		input = &payloadwrite.Input{}
		rows = changes
		field = "Payloads"
		path = "/v1/api/agently/payload"
	default:
		return fmt.Errorf("unknown data deletion %q", kind)
	}
	_, err := s.invokeDataWriter(ctx, input, field, path, rows)
	return err
}
func (s *datlyService) patchTurnQueueNative(ctx context.Context, row *turnqueuemodel.TurnQueue) error {
	if row == nil {
		return fmt.Errorf("turn queue input is required")
	}
	_, err := invokeDataBatch[turnqueuemodel.TurnQueue, queuewrite.TurnQueue](ctx, s, &queuewrite.Input{}, "Queues", "/v1/api/agently/turnqueue", []*turnqueuemodel.TurnQueue{row})
	return err
}
