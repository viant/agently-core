package conversation

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	base "github.com/viant/agently-core/internal/datly/message/base"
	read "github.com/viant/agently-core/internal/datly/message/read"
	write "github.com/viant/agently-core/internal/datly/message/write"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

type MessageStore struct {
	Invoker dexec.ComponentInvoker
	OwnerID func(context.Context) string
}

var messageReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/message"},
}
var messageBaseReaderTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[base.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/message/base"}}
var messageWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/message"},
}

func (s *MessageStore) read(ctx context.Context, mode string, input *read.MessagesInput) (*read.MessageView, error) {
	if s == nil || s.Invoker == nil || s.OwnerID == nil {
		return nil, fmt.Errorf("message component store is not configured")
	}
	owner := strings.TrimSpace(s.OwnerID(ctx))
	providers := []locator.Provider{
		provider.Named("messageaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "internal":
				return true, true, nil
			case "mode":
				return mode, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: messageReaderTarget, Input: input, Providers: providers})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.MessagesOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("message reader returned %T", value)
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	if len(out.Data) != 1 || out.Data[0] == nil {
		return nil, fmt.Errorf("message identity returned %d rows", len(out.Data))
	}
	return out.Data[0], nil
}

// ListRows reads the base message page with Datly-owned filters, projection,
// ordering and limit. The caller applies its conversation authorization rule.
func (s *MessageStore) ListRows(ctx context.Context, input *read.MessagesInput, selectors state.Selectors) ([]*read.MessageView, error) {
	baseRows, err := s.ListBaseRows(ctx, input, selectors)
	if err != nil {
		return nil, err
	}
	rows := make([]*read.MessageView, 0, len(baseRows))
	for _, row := range baseRows {
		rows = append(rows, messageFromBase(row))
	}
	return rows, nil
}

func (s *MessageStore) ListBaseRows(ctx context.Context, input *read.MessagesInput, selectors state.Selectors) ([]*base.MessageBaseView, error) {
	if s == nil || s.Invoker == nil || s.OwnerID == nil {
		return nil, fmt.Errorf("message component store is not configured")
	}
	if input == nil {
		input = &read.MessagesInput{}
	}
	owner := strings.TrimSpace(s.OwnerID(ctx))
	providers := []locator.Provider{
		provider.Named("messageaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "internal":
				return true, true, nil
			case "mode":
				return "rows", true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}
	if len(selectors) > 0 {
		providers = append(providers, queryselectors.ProviderMapped(selectors, map[string]string{"message_rows": "reader", "MessageRows": "reader"}))
	}
	options := dexec.ReaderOptionsFromContext(ctx)
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: messageBaseReaderTarget, Input: input, Providers: providers, ReaderOptions: &options})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*base.MessagesOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("message reader returned %T", value)
	}
	return out.Data, nil
}

func (s *MessageStore) Get(ctx context.Context, id string, modelCalls, toolCalls bool) (*read.MessageView, error) {
	if strings.TrimSpace(id) == "" {
		return nil, nil
	}
	input := &read.MessagesInput{}
	input.SetId(strings.TrimSpace(id))
	if modelCalls {
		input.SetIncludeModelCal(true)
	}
	if toolCalls {
		input.SetIncludeToolCall(true)
	}
	return s.read(ctx, "byId", input)
}

func (s *MessageStore) ByElicitation(ctx context.Context, conversationID, elicitationID string) (*read.MessageView, error) {
	if strings.TrimSpace(conversationID) == "" || strings.TrimSpace(elicitationID) == "" {
		return nil, nil
	}
	input := &read.MessagesInput{}
	input.SetConversationId(strings.TrimSpace(conversationID))
	input.SetElicitationId(strings.TrimSpace(elicitationID))
	return s.read(ctx, "elicitation", input)
}

func (s *MessageStore) ByParentElicitation(ctx context.Context, parentID, elicitationID string) (*read.MessageView, error) {
	if strings.TrimSpace(parentID) == "" || strings.TrimSpace(elicitationID) == "" {
		return nil, nil
	}
	input := &read.MessagesInput{}
	input.SetParentMessageId(strings.TrimSpace(parentID))
	input.SetElicitationId(strings.TrimSpace(elicitationID))
	return s.read(ctx, "parentElicitation", input)
}

func (s *MessageStore) ByLinkedElicitation(ctx context.Context, linkedID, elicitationID string) (*read.MessageView, error) {
	if strings.TrimSpace(linkedID) == "" || strings.TrimSpace(elicitationID) == "" {
		return nil, nil
	}
	input := &read.MessagesInput{}
	input.SetLinkedConversationId(strings.TrimSpace(linkedID))
	input.SetElicitationId(strings.TrimSpace(elicitationID))
	return s.read(ctx, "linkedElicitation", input)
}

func (s *MessageStore) PatchTrusted(ctx context.Context, row *write.Message) error {
	_, err := s.PatchTrustedResult(ctx, row)
	return err
}

// PatchTrustedResult returns successful generated mutation rows, including lifecycle
// defaults and presence, for callers that expose mutable input objects.
func (s *MessageStore) PatchTrustedResult(ctx context.Context, row *write.Message) (*write.Output, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("message component store is not configured")
	}
	if row == nil {
		return nil, fmt.Errorf("message mutation is required")
	}
	input := &write.Input{}
	input.SetMessages([]*write.Message{row})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: messageWriterTarget, Input: input})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*write.Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("message writer returned %T", value)
	}
	return output, nil
}

// DeleteTrusted submits all requested IDs through the canonical generated writer.
func (s *MessageStore) DeleteTrusted(ctx context.Context, ids ...string) error {
	if s == nil || s.Invoker == nil {
		return fmt.Errorf("message component store is not configured")
	}
	rows := make([]*write.Message, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			row := &write.Message{}
			row.SetId(id)
			row.SetShouldDelete(true)
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	input := &write.Input{}
	input.SetMessages(rows)
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: messageWriterTarget, Input: input})
	if err != nil {
		return err
	}
	if _, ok := value.(*write.Output); !ok {
		return fmt.Errorf("message writer returned %T", value)
	}
	return nil
}
