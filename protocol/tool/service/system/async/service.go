// Package async provides the system/async tool service. It exposes a
// single method (`list`) that enumerates non-terminal async operations
// for the current conversation. The conversation scope is read from the
// request context; the LLM never provides or sees conversation ids.
package async

import (
	"context"
	"reflect"
	"strings"

	asynccfg "github.com/viant/agently-core/protocol/async"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// Name is the canonical tool namespace for this service.
const Name = "system/async"

// Service implements the system/async tool surface.
//
// It is stateless; all state lives on the async Manager retrieved from
// the request context (see protocol/async.ManagerFromContext).
type Service struct{}

// New creates a new Service instance.
func New() *Service { return &Service{} }

// Name returns the service name.
func (s *Service) Name() string { return Name }

// CacheableMethods declares which methods produce cacheable outputs.
// `list` is intrinsically volatile (operations change over time), so
// nothing is cached here.
func (s *Service) CacheableMethods() map[string]bool { return map[string]bool{} }

// Methods returns method signatures for this service.
func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{
			Name:        "list",
			Description: "List non-terminal async operations for the current conversation. Optional filters: tool (start-tool name) and mode (\"wait\"|\"detach\"|\"fork\"). Returns each op's id, start tool, status tool, and the arg name to pass the id under — enough to issue a status call without guessing.",
			Input:       reflect.TypeOf(&ListInput{}),
			Output:      reflect.TypeOf(&ListOutput{}),
		},
	}
}

// Method resolves an executable by name.
func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(name) {
	case "list":
		return s.list, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

func (s *Service) list(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ListInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ListOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	return s.List(ctx, input, output)
}

// List populates output with the non-terminal ops for the current
// conversation matching the optional filters. The conversation id is
// read from the request context; callers never provide it.
func (s *Service) List(ctx context.Context, input *ListInput, output *ListOutput) error {
	if output == nil {
		return nil
	}
	manager, ok := asynccfg.ManagerFromContext(ctx)
	if !ok || manager == nil {
		output.Ops = nil
		return nil
	}

	convID := ""
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		convID = strings.TrimSpace(turn.ConversationID)
	}
	if convID == "" {
		// Without a trusted conversation id we refuse to return anything
		// rather than risk a cross-conversation leak. A missing turn
		// context means this tool was invoked outside a normal LLM flow.
		output.Ops = nil
		return nil
	}

	filter := asynccfg.Filter{
		ConversationID: convID,
		Tool:           strings.TrimSpace(input.Tool),
		ExecutionMode:  strings.TrimSpace(input.Mode),
	}
	output.Ops = manager.ListOperations(filter)
	return nil
}
