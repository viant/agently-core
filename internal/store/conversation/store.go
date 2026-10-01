// Package conversation maps trusted callers to the canonical transcribed
// conversation reader and writer. Datly owns selection, joins and mutations.
package conversation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	read "github.com/viant/agently-core/internal/datly/conversation/read"
	write "github.com/viant/agently-core/internal/datly/conversation/write"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

var ErrNotFound = errors.New("conversation not found")

type Store struct {
	Invoker dexec.ComponentInvoker
	OwnerID func(context.Context) string
}

var readerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/conversation/{id}"},
}
var writerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/conversation"},
}

func (s *Store) providers(ctx context.Context, list, ascending, enforceVisibility, graph bool) ([]locator.Provider, error) {
	if s == nil || s.Invoker == nil || s.OwnerID == nil {
		return nil, fmt.Errorf("conversation component store is not configured")
	}
	owner := strings.TrimSpace(s.OwnerID(ctx))
	return []locator.Provider{
		provider.Named("conversationaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "list":
				return list, true, nil
			case "ascending":
				return ascending, true, nil
			case "enforceVisibility":
				return enforceVisibility, true, nil
			case "graph":
				return graph, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}, nil
}

func (s *Store) read(ctx context.Context, input *read.ConversationInput, list, baseOnly bool) ([]*read.ConversationView, error) {
	return s.readPage(ctx, input, list, baseOnly, false, list, 0, false)
}

func (s *Store) readPage(ctx context.Context, input *read.ConversationInput, list, baseOnly, ascending, enforceVisibility bool, limit int, graph bool, selectors ...state.Selectors) ([]*read.ConversationView, error) {
	providers, err := s.providers(ctx, list, ascending, enforceVisibility, graph)
	if err != nil {
		return nil, err
	}
	if input == nil {
		input = &read.ConversationInput{}
	}
	for _, supplied := range selectors {
		if len(supplied) > 0 {
			providers = append(providers, queryselectors.ProviderMapped(supplied, conversationSelectorViews))
		}
	}
	options := dexec.ReaderOptionsFromContext(ctx)
	options.RootOnly = baseOnly
	options.ExcludeFields = nil
	if baseOnly && limit > 0 {
		providers = append(providers, queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Limit: limit}}}))
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: input, Providers: providers, ReaderOptions: &options})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.ConversationOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("conversation reader returned %T", value)
	}
	return out.Data, nil
}

// GetVisible applies the list visibility predicate even to a single ID, so
// absent and inaccessible private conversations have the same result.
func (s *Store) GetVisible(ctx context.Context, id string, input *read.ConversationInput) (*read.ConversationView, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrNotFound
	}
	query := cloneInput(input)
	query.SetId(strings.TrimSpace(id))
	rows, err := s.read(ctx, query, true, false)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	if len(rows) != 1 || rows[0] == nil {
		return nil, fmt.Errorf("conversation identity returned %d rows", len(rows))
	}
	return rows[0], nil
}

// ListVisible returns base conversation rows under the same owner predicate.
func (s *Store) ListVisible(ctx context.Context, input *read.ConversationInput) ([]*read.ConversationView, error) {
	query := cloneInput(input)
	if !query.Has.ParentId && !query.Has.ParentTurnId && !query.Has.ExcludeChildren {
		query.SetExcludeChildren(true)
	}
	return s.read(ctx, query, true, true)
}

// ListPage applies the canonical list predicates and a caller-owned visibility
// decision. Ascending order supports the exclusive "after" cursor window.
func (s *Store) ListPage(ctx context.Context, input *read.ConversationInput, limit int, ascending, enforceVisibility bool) ([]*read.ConversationView, error) {
	query := cloneInput(input)
	if !query.Has.ParentId && !query.Has.ParentTurnId && !query.Has.ExcludeChildren {
		query.SetExcludeChildren(true)
	}
	return s.readPage(ctx, query, true, true, ascending, enforceVisibility, limit, false)
}

// GraphRows reads only base conversation rows for trusted graph traversal.
// A nonempty batch or exact schedule predicate bounds the trusted read.
func (s *Store) GraphRows(ctx context.Context, input *read.ConversationInput, selected ...[]string) ([]*read.ConversationView, error) {
	if input == nil || input.Has == nil ||
		(!input.Has.Ids && !input.Has.ParentIds && !input.Has.ParentTurnIds && !input.Has.ScheduleId && !input.Has.ScheduleRunId) {
		return nil, fmt.Errorf("conversation graph read requires a bounded predicate")
	}
	if input.Has.Ids && len(input.Ids) == 0 || input.Has.ParentIds && len(input.ParentIds) == 0 || input.Has.ParentTurnIds && len(input.ParentTurnIds) == 0 ||
		input.Has.ScheduleId && strings.TrimSpace(input.ScheduleId) == "" || input.Has.ScheduleRunId && strings.TrimSpace(input.ScheduleRunId) == "" {
		return nil, fmt.Errorf("conversation graph read requires nonempty IDs")
	}
	if len(selected) > 0 && len(selected[0]) > 0 {
		providers, err := s.providers(ctx, false, false, false, true)
		if err != nil {
			return nil, err
		}
		providers = append(providers, queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: append([]string(nil), selected[0]...)}}}))
		value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: input, Providers: providers})
		if err != nil {
			return nil, err
		}
		output, ok := value.(*read.ConversationOutput)
		if !ok || output == nil {
			return nil, fmt.Errorf("conversation graph reader returned %T", value)
		}
		return output.Data, nil
	}
	return s.readPage(ctx, input, false, true, false, false, 0, true)
}

// GetInternal is for an already-authorized service caller. It retains the
// complete transcript graph and does not apply the list visibility filter.
func (s *Store) GetInternal(ctx context.Context, id string, input *read.ConversationInput, selectors ...state.Selectors) (*read.ConversationView, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrNotFound
	}
	query := cloneInput(input)
	query.SetId(strings.TrimSpace(id))
	rows, err := s.readPage(ctx, query, false, false, false, false, 0, false, selectors...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	if len(rows) != 1 || rows[0] == nil {
		return nil, fmt.Errorf("conversation identity returned %d rows", len(rows))
	}
	return rows[0], nil
}

var conversationSelectorViews = map[string]string{
	"Conversation": "reader", "conversation": "reader",
	"Transcript": "transcript", "Message": "message", "ToolMessage": "toolMessage",
	"ToolCall": "toolCall", "MessageToolCall": "messageToolCall", "ModelCall": "modelCall",
	"Model": "model", "Usage": "usage", "Attachment": "attachment",
	"LinkedConversation": "linkedConversation", "UserElicitationData": "userElicitationData",
	"RequestPayload": "requestPayload", "ResponsePayload": "responsePayload",
	"MessageRequestPayload": "messageRequestPayload", "MessageResponsePayload": "messageResponsePayload",
	"ModelCallRequestPayload": "modelCallRequestPayload", "ModelCallResponsePayload": "modelCallResponsePayload",
	"ModelCallProviderRequestPayload": "modelCallProviderRequestPayload", "ModelCallProviderResponsePayload": "modelCallProviderResponsePayload",
	"ModelCallStreamPayload": "modelCallStreamPayload", "ToolCallLinks": "toolCallLinks",
}

// GetBaseInternal reads only the conversation row for trusted authorization
// checks. The caller decides whether the returned row is visible.
func (s *Store) GetBaseInternal(ctx context.Context, id string) (*read.ConversationView, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrNotFound
	}
	query := &read.ConversationInput{}
	query.SetId(strings.TrimSpace(id))
	rows, err := s.read(ctx, query, false, true)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	if len(rows) != 1 || rows[0] == nil {
		return nil, fmt.Errorf("conversation identity returned %d rows", len(rows))
	}
	return rows[0], nil
}

// PatchTrusted submits one presence-aware generated mutation after the caller
// has applied its own authorization and event policy.
func (s *Store) PatchTrusted(ctx context.Context, row *write.MutableConversationView) error {
	_, err := s.PatchTrustedResult(ctx, row)
	return err
}

// PatchTrustedResult returns successful generated mutation rows, including lifecycle
// defaults and presence, for callers that expose mutable input objects.
func (s *Store) PatchTrustedResult(ctx context.Context, row *write.MutableConversationView) (*write.Output, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("conversation component store is not configured")
	}
	if row == nil {
		return nil, fmt.Errorf("conversation mutation is required")
	}
	input := &write.Input{}
	input.SetConversations([]*write.MutableConversationView{row})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: writerTarget, Input: input})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*write.Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("conversation writer returned %T", value)
	}
	return output, nil
}

// DeleteTrusted uses one generated writer invocation for the complete batch.
// The caller remains responsible for authorization and tree-delete policy.
func (s *Store) DeleteTrusted(ctx context.Context, ids ...string) error {
	if s == nil || s.Invoker == nil {
		return fmt.Errorf("conversation component store is not configured")
	}
	rows := make([]*write.MutableConversationView, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			row := &write.MutableConversationView{}
			row.SetId(id)
			row.SetShouldDelete(true)
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	input := &write.Input{}
	input.SetConversations(rows)
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: writerTarget, Input: input})
	if err != nil {
		return err
	}
	if _, ok := value.(*write.Output); !ok {
		return fmt.Errorf("conversation writer returned %T", value)
	}
	return nil
}

func cloneInput(input *read.ConversationInput) *read.ConversationInput {
	if input == nil {
		return &read.ConversationInput{Has: &read.ConversationInputHas{}}
	}
	copy := *input
	if input.Has == nil {
		copy.Has = &read.ConversationInputHas{}
	} else {
		markers := *input.Has
		copy.Has = &markers
	}
	return &copy
}
