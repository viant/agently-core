package conversationtree

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"

	messagemeta "github.com/viant/agently-core/internal/datly/message/cleanup/read"
	messageread "github.com/viant/agently-core/internal/datly/message/read"
	modelcallmeta "github.com/viant/agently-core/internal/datly/modelcall/cleanup/read"
	modelcallread "github.com/viant/agently-core/internal/datly/modelcall/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	toolcallmeta "github.com/viant/agently-core/internal/datly/toolcall/cleanup/read"
	toolcallread "github.com/viant/agently-core/internal/datly/toolcall/read"
	turnmeta "github.com/viant/agently-core/internal/datly/turn/cleanup/read"
	turnread "github.com/viant/agently-core/internal/datly/turn/read"
	"github.com/viant/agently-core/internal/store/conversation"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

// MetadataReaderEnvironment only selects private deletion metadata readers.
// Public transcript/list/runtime readers are deliberately unchanged.
const MetadataReaderEnvironment = "AGENTLY_DELETE_METADATA_READER"

type metadataReaderContextKey struct{}
type metadataReaderMode string

const (
	metadataReaderLegacy  metadataReaderMode = "legacy"
	metadataReaderCompact metadataReaderMode = "compact"
)

func PinMetadataReader(ctx context.Context) (context.Context, error) {
	if _, ok := ctx.Value(metadataReaderContextKey{}).(metadataReaderMode); ok {
		return ctx, nil
	}
	mode := metadataReaderMode(strings.TrimSpace(os.Getenv(MetadataReaderEnvironment)))
	if mode == "" {
		mode = metadataReaderCompact
	}
	if mode != metadataReaderLegacy && mode != metadataReaderCompact {
		return ctx, fmt.Errorf("%s must be legacy or compact, got %q", MetadataReaderEnvironment, mode)
	}
	return context.WithValue(ctx, metadataReaderContextKey{}, mode), nil
}

// A selected but empty ID list is a no-op, never an unfiltered SELECT.
func metadataFilter(marker any, allowed map[string][]string) (string, []string, error) {
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
	if key == "" {
		return "", nil, fmt.Errorf("cleanup metadata read requires a bounded predicate")
	}
	for _, id := range allowed[key] {
		if strings.TrimSpace(id) == "" {
			return "", nil, fmt.Errorf("cleanup metadata identity is empty")
		}
	}
	return key, normalizeIDs(allowed[key]), nil
}

func ReadCleanupTurns(ctx context.Context, invoker dexec.ComponentInvoker, input *turnread.TurnRowsInput, lock bool) ([]*turnread.TurnRowsView, error) {
	if input == nil || invoker == nil {
		return nil, fmt.Errorf("cleanup turn reader is not configured")
	}
	key, ids, err := metadataFilter(input.Has, map[string][]string{"TurnId": {input.TurnId},
		"ConversationID":      {input.ConversationID},
		"ConversationIDs":     input.ConversationIDs,
		"GoalIDs":             input.GoalIDs,
		"StartedByMessageIDs": input.StartedByMessageIDs,
		"RetryOfIDs":          input.RetryOfIDs})
	if err != nil {
		return nil, err
	}
	var rows []*turnread.TurnRowsView
	err = cleanupReadBatches(ctx, ids, func(ids []string) error {
		query := &turnmeta.Input{}
		switch key {
		case "TurnId":
			query.SetIDs(ids)
		case "ConversationID":
			query.SetConversationIDs(ids)
		case "ConversationIDs":
			query.SetConversationIDs(ids)
		case "GoalIDs":
			query.SetGoalIDs(ids)
		case "StartedByMessageIDs":
			query.SetStartedByMessageIDs(ids)
		case "RetryOfIDs":
			query.SetRetryOfIDs(ids)
		}
		target := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[turnmeta.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/turn/cleanup"}}
		value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: target, Input: query,
			Providers: cleanupScope("turncleanupscope"), ReaderOptions: queryselectors.ForUpdateOptions(ctx, lock)})
		if err != nil {
			return err
		}
		out, ok := value.(*turnmeta.Output)
		if !ok || out == nil {
			return fmt.Errorf("cleanup turn reader returned %T", value)
		}
		for _, row := range out.Data {
			if row != nil {
				rows = append(rows, &turnread.TurnRowsView{Id: row.Id, ConversationId: row.ConversationId, RunId: row.RunId, StartedByMessageId: row.StartedByMessageId, RetryOf: row.RetryOf})
			}
		}
		return nil
	})
	return rows, err
}

func ReadCleanupMessages(ctx context.Context, invoker dexec.ComponentInvoker, input *messageread.MessagesInput, lock bool) ([]*messageread.MessageView, error) {
	if input == nil || invoker == nil {
		return nil, fmt.Errorf("cleanup message reader is not configured")
	}
	key, ids, err := metadataFilter(input.Has, map[string][]string{"Id": {input.Id},
		"ConversationId":        {input.ConversationId},
		"ConversationIds":       input.ConversationIds,
		"LinkedConversationId":  {input.LinkedConversationId},
		"LinkedConversationIds": input.LinkedConversationIds,
		"ParentMessageId":       {input.ParentMessageId},
		"ParentMessageIds":      input.ParentMessageIds,
		"SupersededByIds":       input.SupersededByIds})
	if err != nil {
		return nil, err
	}
	var rows []*messageread.MessageView
	err = cleanupReadBatches(ctx, ids, func(ids []string) error {
		query := &messagemeta.Input{}
		switch key {
		case "Id":
			query.SetIDs(ids)
		case "ConversationId":
			query.SetConversationIDs(ids)
		case "ConversationIds":
			query.SetConversationIDs(ids)
		case "LinkedConversationId":
			query.SetLinkedConversationIDs(ids)
		case "LinkedConversationIds":
			query.SetLinkedConversationIDs(ids)
		case "ParentMessageId":
			query.SetParentMessageIDs(ids)
		case "ParentMessageIds":
			query.SetParentMessageIDs(ids)
		case "SupersededByIds":
			query.SetSupersededByIDs(ids)
		}
		target := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[messagemeta.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/message/cleanup"}}
		value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: target, Input: query,
			Providers: cleanupScope("messagecleanupscope"), ReaderOptions: queryselectors.ForUpdateOptions(ctx, lock)})
		if err != nil {
			return err
		}
		out, ok := value.(*messagemeta.Output)
		if !ok || out == nil {
			return fmt.Errorf("cleanup message reader returned %T", value)
		}
		for _, row := range out.Data {
			if row != nil {
				rows = append(rows, &messageread.MessageView{Id: row.Id, ConversationId: row.ConversationId, LinkedConversationId: row.LinkedConversationId, ParentMessageId: row.ParentMessageId, SupersededBy: row.SupersededBy, AttachmentPayloadId: row.AttachmentPayloadId, ElicitationPayloadId: row.ElicitationPayloadId})
			}
		}
		return nil
	})
	return rows, err
}

func ReadCleanupModels(ctx context.Context, invoker dexec.ComponentInvoker, input *modelcallread.ModelCallsInput, lock bool) ([]*modelcallread.ModelCallView, error) {
	if input == nil || invoker == nil {
		return nil, fmt.Errorf("cleanup modelcall reader is not configured")
	}
	key, ids, err := metadataFilter(input.Has, map[string][]string{"MessageIds": input.MessageIds,
		"TurnIds": input.TurnIds})
	if err != nil {
		return nil, err
	}
	var rows []*modelcallread.ModelCallView
	err = cleanupReadBatches(ctx, ids, func(ids []string) error {
		query := &modelcallmeta.Input{}
		switch key {
		case "MessageIds":
			query.SetMessageIDs(ids)
		case "TurnIds":
			query.SetTurnIDs(ids)
		}
		target := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[modelcallmeta.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/modelcall/cleanup"}}
		value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: target, Input: query,
			Providers: cleanupScope("modelcallcleanupscope"), ReaderOptions: queryselectors.ForUpdateOptions(ctx, lock)})
		if err != nil {
			return err
		}
		out, ok := value.(*modelcallmeta.Output)
		if !ok || out == nil {
			return fmt.Errorf("cleanup modelcall reader returned %T", value)
		}
		for _, row := range out.Data {
			if row != nil {
				rows = append(rows, &modelcallread.ModelCallView{MessageId: row.MessageId, TurnId: row.TurnId, RunId: row.RunId, RequestPayloadId: row.RequestPayloadId, ResponsePayloadId: row.ResponsePayloadId, ProviderRequestPayloadId: row.ProviderRequestPayloadId, ProviderResponsePayloadId: row.ProviderResponsePayloadId, StreamPayloadId: row.StreamPayloadId})
			}
		}
		return nil
	})
	return rows, err
}

func ReadCleanupTools(ctx context.Context, invoker dexec.ComponentInvoker, input *toolcallread.ToolCallsInput, lock bool) ([]*toolcallread.ToolCallView, error) {
	if input == nil || invoker == nil {
		return nil, fmt.Errorf("cleanup toolcall reader is not configured")
	}
	key, ids, err := metadataFilter(input.Has, map[string][]string{"MessageIds": input.MessageIds,
		"TurnIds": input.TurnIds})
	if err != nil {
		return nil, err
	}
	var rows []*toolcallread.ToolCallView
	err = cleanupReadBatches(ctx, ids, func(ids []string) error {
		query := &toolcallmeta.Input{}
		switch key {
		case "MessageIds":
			query.SetMessageIDs(ids)
		case "TurnIds":
			query.SetTurnIDs(ids)
		}
		target := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[toolcallmeta.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/toolcall/cleanup"}}
		value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: target, Input: query,
			Providers: cleanupScope("toolcallcleanupscope"), ReaderOptions: queryselectors.ForUpdateOptions(ctx, lock)})
		if err != nil {
			return err
		}
		out, ok := value.(*toolcallmeta.Output)
		if !ok || out == nil {
			return fmt.Errorf("cleanup toolcall reader returned %T", value)
		}
		for _, row := range out.Data {
			if row != nil {
				rows = append(rows, &toolcallread.ToolCallView{MessageId: row.MessageId, OpId: row.OpId, TurnId: row.TurnId, RunId: row.RunId, RequestPayloadId: row.RequestPayloadId, ResponsePayloadId: row.ResponsePayloadId})
			}
		}
		return nil
	})
	return rows, err
}

func (d *Discoverer) turnRows(ctx context.Context, input *turnread.TurnRowsInput, selectors state.Selectors) ([]*turnread.TurnRowsView, error) {
	ctx, err := PinMetadataReader(ctx)
	if err != nil {
		return nil, err
	}
	if ctx.Value(metadataReaderContextKey{}) == metadataReaderCompact {
		return ReadCleanupTurns(ctx, d.Invoker, input, false)
	}
	return (&conversation.TurnStore{Invoker: d.Invoker}).ListRows(ctx, input, selectors)
}
func (d *Discoverer) messageRows(ctx context.Context, input *messageread.MessagesInput, selectors state.Selectors) ([]*messageread.MessageView, error) {
	ctx, err := PinMetadataReader(ctx)
	if err != nil {
		return nil, err
	}
	if ctx.Value(metadataReaderContextKey{}) == metadataReaderCompact {
		return ReadCleanupMessages(ctx, d.Invoker, input, len(dexec.ReaderOptionsFromContext(ctx).ForUpdate) > 0)
	}
	return (&conversation.MessageStore{Invoker: d.Invoker, OwnerID: d.OwnerID}).ListRows(ctx, input, selectors)
}
func (d *Discoverer) modelRows(ctx context.Context, input *modelcallread.ModelCallsInput, fields ...string) ([]*modelcallread.ModelCallView, error) {
	ctx, err := PinMetadataReader(ctx)
	if err != nil {
		return nil, err
	}
	if ctx.Value(metadataReaderContextKey{}) == metadataReaderCompact {
		return ReadCleanupModels(ctx, d.Invoker, input, false)
	}
	return planReaderRows[modelcallread.ModelCallView](ctx, d, input, "/v1/internal/agently/model-call", planProviders("modelcallaccess", d.OwnerID(ctx), fields...))
}
func (d *Discoverer) toolRows(ctx context.Context, input *toolcallread.ToolCallsInput, fields ...string) ([]*toolcallread.ToolCallView, error) {
	ctx, err := PinMetadataReader(ctx)
	if err != nil {
		return nil, err
	}
	if ctx.Value(metadataReaderContextKey{}) == metadataReaderCompact {
		return ReadCleanupTools(ctx, d.Invoker, input, false)
	}
	return planReaderRows[toolcallread.ToolCallView](ctx, d, input, "/v1/internal/agently/tool-call", planProviders("toolcallaccess", d.OwnerID(ctx), fields...))
}
func (d *Discoverer) detachTurnRows(ctx context.Context, input *turnread.TurnRowsInput) ([]*turnread.TurnRowsView, error) {
	ctx, err := PinMetadataReader(ctx)
	if err != nil {
		return nil, err
	}
	if ctx.Value(metadataReaderContextKey{}) == metadataReaderCompact {
		return ReadCleanupTurns(ctx, d.Invoker, input, d.LockDetachRows)
	}
	return planReaderRows[turnread.TurnRowsView](ctx, d, input, "/v1/api/agently/turn/list/list", d.planDetachProviders("turnaccess", []string{"id", "started_by_message_id", "retry_of"}), d.LockDetachRows)
}
