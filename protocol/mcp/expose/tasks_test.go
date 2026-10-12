package expose

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	tasks "github.com/viant/mcp-tasks"
	taskrpc "github.com/viant/mcp-tasks/rpc"
	"testing"
	"time"
)

type tasksNext struct {
	calls         int
	notifications int
}

func (n *tasksNext) Serve(_ context.Context, request *jsonrpc.Request, response *jsonrpc.Response) {
	n.calls++
	response.Result = json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"ordinary admission receipt"}]}`)
}
func (n *tasksNext) OnNotification(context.Context, *jsonrpc.Notification) { n.notifications++ }

type tasksNative struct{ creates int }

func (b *tasksNative) Create(_ context.Context, _ tasks.Owner, _ schema.ClientCapabilities, options tasks.CreateOptions) (tasks.CreateTaskResult, error) {
	b.creates++
	now := time.Now()
	return tasks.CreateTaskResult{ResultType: "task", DetailedTask: tasks.DetailedTask{Task: tasks.Task{TaskID: "owned-native-alias", Status: tasks.Working, CreatedAt: now, LastUpdatedAt: now, TTLMS: nil}}}, nil
}
func (*tasksNative) Get(context.Context, tasks.Owner, string) (tasks.GetTaskResult, error) {
	return tasks.GetTaskResult{}, tasks.ErrNotFound
}
func (*tasksNative) Update(context.Context, tasks.Owner, string, tasks.InputResponses) (tasks.Acknowledgement, error) {
	return tasks.Acknowledgement{ResultType: "complete"}, nil
}
func (*tasksNative) Cancel(context.Context, tasks.Owner, string) (tasks.Acknowledgement, error) {
	return tasks.Acknowledgement{ResultType: "complete"}, nil
}
func TestTasksWrapperPreservesNonNegotiatedCallsAndCapturesNativeOnce(t *testing.T) {
	next := &tasksNext{}
	backend := &tasksNative{}
	captures := 0
	resolver := taskrpc.OwnerResolver(func(context.Context, *jsonrpc.Request) (tasks.Owner, error) {
		return tasks.Owner{Tenant: "workspace", Subject: "owner"}, nil
	})
	wrapper, err := WithTasks(next, backend, resolver, func(_ context.Context, _ *jsonrpc.Request, response *jsonrpc.Response) (*tasks.CreateOptions, error) {
		captures++
		if next.calls == 0 {
			t.Fatal("capture before native execution")
		}
		return &tasks.CreateOptions{Origin: json.RawMessage(`{"nativeRunId":"already-admitted"}`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	request := &jsonrpc.Request{Jsonrpc: "2.0", Id: 1, Method: tasks.MethodCallTool, Params: json.RawMessage(`{"name":"fixture"}`)}
	response := &jsonrpc.Response{}
	wrapper.Serve(context.Background(), request, response)
	if captures != 0 || backend.creates != 0 || next.calls != 1 || response.Error != nil {
		t.Fatal("non-negotiated behavior changed")
	}
	caps := schema.ClientCapabilities{}
	tasks.Advertise(&caps)
	request.Params, _ = json.Marshal(map[string]any{"name": "fixture", "_meta": map[string]any{tasks.ClientCapabilitiesMeta: caps}})
	response = &jsonrpc.Response{}
	wrapper.Serve(context.Background(), request, response)
	if response.Error == nil || next.calls != 1 || captures != 0 {
		t.Fatal("invalid negotiated metadata admitted execution")
	}
	request.Params, _ = json.Marshal(map[string]any{"name": "fixture", "_meta": map[string]any{tasks.ClientCapabilitiesMeta: caps, "io.modelcontextprotocol/protocolVersion": tasks.ProtocolVersion, "io.modelcontextprotocol/clientInfo": schema.Implementation{Name: "fixture", Version: "1"}}})
	response = &jsonrpc.Response{}
	wrapper.Serve(context.Background(), request, response)
	if captures != 1 || backend.creates != 1 || next.calls != 2 || response.Error != nil {
		t.Fatal("native capture dispatched twice", response.Error)
	}
	task, err := tasks.DecodeCreate(response.Result)
	if err != nil || task.TaskID != "owned-native-alias" {
		t.Fatal("wire handle", err)
	}
}

type tasksObserved struct{ tasksNative }

func (*tasksObserved) Get(context.Context, tasks.Owner, string) (tasks.GetTaskResult, error) {
	now := time.Now()
	return tasks.GetTaskResult{ResultType: tasks.ResultComplete, DetailedTask: tasks.DetailedTask{Task: tasks.Task{TaskID: "owned-native-alias", Status: tasks.Working, CreatedAt: now, LastUpdatedAt: now, TTLMS: nil}}}, nil
}

func TestTasksWrapperRoutesStdioCancellationToOwnedSubscription(t *testing.T) {
	next := &tasksNext{}
	ack := make(chan struct{}, 1)
	resolver := func(context.Context, *jsonrpc.Request) (tasks.Owner, error) {
		return tasks.Owner{Tenant: "workspace", Subject: "owner"}, nil
	}
	notifier := &taskrpc.SerialNotifier{NotifyFunc: func(_ context.Context, n *jsonrpc.Notification) error {
		if n.Method == tasks.MethodAcknowledged {
			ack <- struct{}{}
		}
		return nil
	}}
	wrapper, err := WithTasks(next, &tasksObserved{}, resolver, func(context.Context, *jsonrpc.Request, *jsonrpc.Response) (*tasks.CreateOptions, error) {
		return nil, nil
	}, WithTaskSubscriptions(notifier, taskrpc.SubscriptionOptions{MaxDuration: time.Minute}))
	if err != nil {
		t.Fatal(err)
	}
	caps := schema.ClientCapabilities{}
	tasks.Advertise(&caps)
	params, _ := json.Marshal(map[string]any{"notifications": map[string]any{"taskIds": []string{"owned-native-alias"}}, "_meta": map[string]any{tasks.ClientCapabilitiesMeta: caps, "io.modelcontextprotocol/protocolVersion": tasks.ProtocolVersion, "io.modelcontextprotocol/clientInfo": schema.Implementation{Name: "fixture", Version: "1"}}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	response := &jsonrpc.Response{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		wrapper.Serve(ctx, &jsonrpc.Request{Jsonrpc: "2.0", Id: 41, Method: tasks.MethodListen, Params: params}, response)
	}()
	select {
	case <-ack:
	case <-time.After(time.Second):
		t.Fatal("subscription was not acknowledged")
	}
	wrapper.OnNotification(ctx, &jsonrpc.Notification{Jsonrpc: "2.0", Method: "notifications/cancelled", Params: json.RawMessage(`{"requestId":41}`)})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wrapper dropped observer cancellation")
	}
	if response.Error != nil || next.notifications != 0 {
		t.Fatal("subscription cancellation escaped to ordinary executor", response.Error)
	}
	wrapper.OnNotification(ctx, &jsonrpc.Notification{Jsonrpc: "2.0", Method: "notifications/initialized"})
	if next.notifications != 1 {
		t.Fatal("ordinary notification was not delegated")
	}
}
