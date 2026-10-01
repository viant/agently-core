package data

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	gfread "github.com/viant/agently-core/internal/datly/generatedfile/read"
	msgread "github.com/viant/agently-core/internal/datly/message/read"
	payloadread "github.com/viant/agently-core/internal/datly/payload/reference"
	toolread "github.com/viant/agently-core/internal/datly/toolcall/read"
	turnread "github.com/viant/agently-core/internal/datly/turn/read"
	queueread "github.com/viant/agently-core/internal/datly/turnqueue/read"
	convstore "github.com/viant/agently-core/internal/store/conversation"
	queuestore "github.com/viant/agently-core/internal/store/turnqueue"
	legacygf "github.com/viant/agently-core/pkg/agently/generatedfile/read"
	legacymsg "github.com/viant/agently-core/pkg/agently/message"
	legacyelicitation "github.com/viant/agently-core/pkg/agently/message/elicitation"
	legacypayload "github.com/viant/agently-core/pkg/agently/payload"
	legacytool "github.com/viant/agently-core/pkg/agently/toolcall/byOp"
	legacytoolturn "github.com/viant/agently-core/pkg/agently/toolcall/byTurn"
	legacyqueue "github.com/viant/agently-core/pkg/agently/turnqueue/read"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/handler"
	"github.com/viant/xdatly/state"
)

func (s *datlyService) readDataNative(ctx context.Context, input any, path, access, mode string, opts *options) (any, error) {
	if s == nil || s.native == nil {
		return nil, fmt.Errorf("native data runtime is required")
	}
	owner := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if opts != nil && opts.principal != "" {
		owner = opts.principal
	}
	providers := []locator.Provider{provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil })}
	if access != "" {
		providers = append(providers, provider.Named(access, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "internal":
				return true, true, nil
			case "mode":
				return mode, true, nil
			}
			return nil, false, nil
		}))
	}
	// Public selector names are mapped to the canonical reader by the provider.
	if opts != nil && len(opts.selectors) > 0 {
		providers = append(providers, dataSelectorsProvider(opts.selectors))
	}
	return s.native.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeOf(input).Elem().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: path}}, Input: input, Providers: providers})
}

func (s *datlyService) getMessageNative(ctx context.Context, id string, input *legacymsg.MessageInput, opts *options) (*legacymsg.MessageView, error) {
	query, err := mapDataDTO[msgread.MessagesInput](input)
	if err != nil {
		return nil, err
	}
	if query == nil {
		query = &msgread.MessagesInput{}
	}
	query.SetId(id)
	value, err := s.readDataNative(ctx, query, "/v1/internal/agently/message", "messageaccess", "byId", opts)
	if err != nil {
		return nil, err
	}
	output, ok := value.(*msgread.MessagesOutput)
	if !ok {
		return nil, fmt.Errorf("message reader returned %T", value)
	}
	if len(output.Data) == 0 {
		return nil, nil
	}
	if err = s.authorizeConversationID(ctx, output.Data[0].ConversationId, opts, nil); err != nil {
		return nil, err
	}
	return mapDataDTO[legacymsg.MessageView](output.Data[0])
}
func (s *datlyService) getMessageByElicitationNative(ctx context.Context, conversationID, elicitationID string, opts *options) (*legacyelicitation.MessageView, error) {
	if err := s.authorizeConversationID(ctx, conversationID, opts, nil); err != nil {
		return nil, err
	}
	component := &convstore.MessageStore{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	row, err := component.ByElicitation(ctx, conversationID, elicitationID)
	if err != nil {
		return nil, err
	}
	return mapDataDTO[legacyelicitation.MessageView](row)
}
func (s *datlyService) readTurnNative(ctx context.Context, mode string, input any, opts *options) ([]*turnread.TurnRowsView, error) {
	query, err := mapDataDTO[turnread.TurnRowsInput](input)
	if err != nil {
		return nil, err
	}
	if query == nil {
		query = &turnread.TurnRowsInput{}
	}
	if (mode == "queued" || mode == "nextQueued") && opts != nil && opts.principal != "" && !opts.isAdmin && strings.TrimSpace(query.ConversationID) == "" {
		return nil, ErrPermissionDenied
	}
	if mode == "byId" && input != nil {
		v := reflect.ValueOf(input)
		if v.Kind() == reflect.Pointer && !v.IsNil() {
			id := v.Elem().FieldByName("ID")
			has := v.Elem().FieldByName("Has")
			if id.IsValid() && has.IsValid() && !has.IsNil() && has.Elem().FieldByName("ID").Bool() {
				query.SetTurnId(id.String())
			}
		}
	}
	if query.ConversationID != "" {
		if err = s.authorizeConversationID(ctx, query.ConversationID, opts, nil); err != nil {
			return nil, err
		}
	}
	value, err := s.readDataNative(ctx, query, "/v1/api/agently/turn/list/list", "turnaccess", mode, opts)
	if err != nil {
		return nil, err
	}
	out, ok := value.(*turnread.TurnRowsOutput)
	if !ok {
		return nil, fmt.Errorf("turn reader returned %T", value)
	}
	for _, row := range out.Data {
		if row != nil {
			if err = s.authorizeConversationID(ctx, row.ConversationId, opts, nil); err != nil {
				return nil, err
			}
		}
	}
	return out.Data, nil
}
func (s *datlyService) getToolCallByOpNative(ctx context.Context, opID string, input *legacytool.ToolCallRowsInput, opts *options) ([]*legacytool.ToolCallRowsView, error) {
	query, err := mapDataDTO[toolread.ToolCallsInput](input)
	if err != nil {
		return nil, err
	}
	if query == nil {
		query = &toolread.ToolCallsInput{}
	}
	query.SetOpId(opID)
	conversationID := ""
	if input != nil {
		conversationID = input.ConversationId
	}
	query.SetConversationId(conversationID)
	value, err := s.readDataNative(ctx, query, "/v1/internal/agently/tool-call", "toolcallaccess", "scopedByOp", opts)
	if err != nil {
		return nil, err
	}
	out, ok := value.(*toolread.ToolCallsOutput)
	if !ok {
		return nil, fmt.Errorf("tool call reader returned %T", value)
	}
	return mapDataDTOs[legacytool.ToolCallRowsView](out.Data)
}
func (s *datlyService) toolCallsByTurnNative(ctx context.Context, conversationID, turnID string) ([]*legacytoolturn.ToolCallRowsView, error) {
	query := &toolread.ToolCallsInput{}
	query.SetConversationId(strings.TrimSpace(conversationID))
	query.SetTurnId(strings.TrimSpace(turnID))
	value, err := s.readDataNative(ctx, query, "/v1/internal/agently/tool-call", "toolcallaccess", "byTurn", nil)
	if err != nil {
		return nil, err
	}
	out, ok := value.(*toolread.ToolCallsOutput)
	if !ok {
		return nil, fmt.Errorf("tool call reader returned %T", value)
	}
	return mapDataDTOs[legacytoolturn.ToolCallRowsView](out.Data)
}
func (s *datlyService) listPayloadsNative(ctx context.Context, input *legacypayload.PayloadRowsInput, opts *options) ([]*legacypayload.PayloadRowsView, error) {
	query, err := mapDataDTO[payloadread.Input](input)
	if err != nil {
		return nil, err
	}
	if query == nil {
		query = &payloadread.Input{}
	}
	value, err := s.readDataNative(ctx, query, "/v2/api/agently/payload", "", "", opts)
	if err != nil {
		return nil, err
	}
	out, ok := value.(*payloadread.Output)
	if !ok {
		return nil, fmt.Errorf("payload reader returned %T", value)
	}
	return mapDataDTOs[legacypayload.PayloadRowsView](out.Data)
}
func (s *datlyService) listGeneratedFilesNative(ctx context.Context, conversationID string, opts *options) ([]*legacygf.GeneratedFileView, error) {
	if err := s.authorizeConversationID(ctx, conversationID, opts, nil); err != nil {
		return nil, err
	}
	query := &gfread.Input{}
	query.SetConversationID(conversationID)
	rows, err := (&convstore.GeneratedFileStore{Invoker: s.native}).List(ctx, query)
	if err != nil {
		return nil, err
	}
	return mapDataDTOs[legacygf.GeneratedFileView](rows)
}
func (s *datlyService) listTurnQueueNative(ctx context.Context, input *legacyqueue.QueueRowsInput, opts *options) ([]*legacyqueue.QueueRowView, error) {
	query, err := mapDataDTO[queueread.QueueRowsInput](input)
	if err != nil {
		return nil, err
	}
	if query == nil {
		query = &queueread.QueueRowsInput{}
	}
	rows, err := (&queuestore.Store{Invoker: s.native}).List(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row != nil {
			if err = s.authorizeConversationID(ctx, row.ConversationId, opts, nil); err != nil {
				return nil, err
			}
		}
	}
	return mapDataDTOs[legacyqueue.QueueRowView](rows)
}

// dataSelectorsProvider maps the legacy facade's named selectors to the stock
// reader selector. It preserves projection, ordering and pagination together.
func dataSelectorsProvider(selectors []*state.NamedSelector) locator.Provider {
	return provider.New(handler.SelectorsKey, func(ctx context.Context) (any, bool, error) {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		mapped := nativeSelectors(selectors)
		for _, selector := range mapped {
			if selector == nil {
				continue
			}
			switch selector.Name {
			case "MessageRows", "message_rows", "message", "TurnRows", "turn_rows", "turn_lookup", "active_turns", "queued_turns", "queued_turn", "QueuedTurns", "QueuedTurn", "ActiveTurns", "TurnLookup", "tool_call_rows", "ToolCallRows", "payload_rows", "PayloadRows":
				selector.Name = "reader"
			}
		}
		return mapped, true, nil
	})
}
