package expose

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	tasks "github.com/viant/mcp-tasks"
	taskrpc "github.com/viant/mcp-tasks/rpc"
)

// TaskCapture is a trusted read-only lookup of the canonical native execution
// just admitted by the ordinary tool path. It must retain its exact causal and
// ownership binding, and never derive authority from result/caller JSON alone.
type TaskCapture func(context.Context, *jsonrpc.Request, *jsonrpc.Response) (*tasks.CreateOptions, error)
type tasksHandler struct {
	next    transport.Handler
	tasks   *taskrpc.Handler
	capture TaskCapture
}

// WithTasks wraps an already authorized MCP transport handler. Owner must apply
// the host's actual authentication; Capture must apply the same exported-tool
// policy. Ordinary non-negotiated calls delegate unchanged. This wrapper does
// not start an executor, alter fork/detach modes or approve a tool.
func WithTasks(next transport.Handler, backend tasks.Backend, owner taskrpc.OwnerResolver, capture TaskCapture, options ...TasksOption) (transport.Handler, error) {
	if next == nil || backend == nil || owner == nil || capture == nil {
		return nil, fmt.Errorf("MCP task handler, native backend, owner and capture are required")
	}
	handler := &tasksHandler{next: next, tasks: &taskrpc.Handler{Next: next, Service: backend, Owner: owner}, capture: capture}
	for _, option := range options {
		if option != nil {
			option(handler.tasks)
		}
	}
	return handler, nil
}
func (h *tasksHandler) Serve(ctx context.Context, request *jsonrpc.Request, response *jsonrpc.Response) {
	if request == nil || response == nil {
		return
	}
	if request.Method != tasks.MethodCallTool {
		h.tasks.Serve(ctx, request, response)
		return
	}
	caps, err := tasks.RequestCapabilities(request.Params)
	if err != nil {
		response.Error = jsonrpc.NewInvalidParamsError("invalid request capabilities", nil)
		return
	}
	if !tasks.Supports(caps) {
		h.next.Serve(ctx, request, response)
		return
	}
	if request.Jsonrpc != jsonrpc.Version || !taskrpc.ValidRequestID(request.Id) || !taskrpc.ValidRequestMetadata(request.Params) {
		response.Error = jsonrpc.NewInvalidParamsError("July per-request metadata required", nil)
		return
	}
	owner, err := h.tasks.Owner(ctx, request)
	if err != nil || !owner.Valid() {
		response.Error = jsonrpc.NewInvalidParamsError("task authority unavailable", nil)
		return
	}
	// Normal policy/admission/execution occurs exactly once. The native runtime
	// already owns deferred work; the extension captures its durable reference.
	h.next.Serve(ctx, request, response)
	if response.Error != nil {
		return
	}
	options, err := h.capture(ctx, request, response)
	if err != nil {
		response.Result = nil
		response.Error = jsonrpc.NewInternalError("native task capture failed; original work must not be replayed", nil)
		return
	}
	if options == nil {
		return
	}
	options.Method = tasks.MethodCallTool
	result, err := h.tasks.Service.Create(ctx, owner, caps, *options)
	if err != nil {
		response.Result = nil
		response.Error = jsonrpc.NewInternalError("native task alias failed; original work must not be replayed", nil)
		return
	}
	response.Result, err = json.Marshal(result)
	if err != nil {
		response.Error = jsonrpc.NewInternalError("task result encoding failed", nil)
	}
}
func (h *tasksHandler) OnNotification(ctx context.Context, notification *jsonrpc.Notification) {
	h.tasks.OnNotification(ctx, notification)
}

// TasksOption enables optional host transport features without claiming them
// on a backend lacking an actual notification route.
type TasksOption func(*taskrpc.Handler)

func WithTaskSubscriptions(notifier transport.Notifier, options taskrpc.SubscriptionOptions) TasksOption {
	return func(handler *taskrpc.Handler) {
		if notifier != nil {
			handler.Notifier = notifier
			handler.Subscriptions = &options
		}
	}
}
