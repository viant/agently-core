package sdk

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"sync"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/mcp-protocol/schema"
	tasks "github.com/viant/mcp-tasks"
)

const mcpTaskAliasMarker = "agently.mcp-task.alias"

// MCPTaskReference is a trusted native reference captured after normal native
// admission/execution. It is never accepted from MCP request parameters. For a
// detached child, capture the existing child reference, not its finished parent.
type MCPTaskReference struct {
	ThreadID       string `json:"threadId"`
	RunID          string `json:"runId"`
	ConversationID string `json:"conversationId,omitempty"`
	TurnID         string `json:"turnId,omitempty"`
	ParentRunID    string `json:"parentRunId,omitempty"`
	InvocationID   string `json:"invocationId,omitempty"`
	OperationID    string `json:"operationId,omitempty"`
	ExecutionMode  string `json:"executionMode,omitempty"`
}

// MCPTaskNativeSnapshot is a projection of the canonical runtime, not a new
// execution record. Result must be the original complete MCP result object.
type MCPTaskNativeSnapshot struct {
	Status        tasks.Status
	StatusMessage string
	UpdatedAt     time.Time
	Result        json.RawMessage
	Error         *tasks.RPCError
	Interrupts    []agui.WireInterrupt
}

// MCPTaskControls delegates to the existing native ownership/policy,
// input/resume and cancellation APIs. Callbacks must not replay execution.
type MCPTaskControls struct {
	Watch     func(context.Context, tasks.Owner, []MCPTaskReference) (tasks.Wakeup, error)
	Authorize func(context.Context, tasks.Owner, MCPTaskReference) error
	Snapshot  func(context.Context, tasks.Owner, MCPTaskReference) (MCPTaskNativeSnapshot, error)
	Resume    func(context.Context, tasks.Owner, MCPTaskReference, []agui.WireResumeEntry) error
	Cancel    func(context.Context, tasks.Owner, MCPTaskReference) error
}
type MCPTasksOptions struct {
	Tenant         string
	TTLMS          *int64
	PollIntervalMS int64
	Clock          func() time.Time
}
type MCPTasksBackend struct {
	store    aguistore.Store
	controls MCPTaskControls
	options  MCPTasksOptions
}
type mcpTaskAlias struct {
	Version        string           `json:"version"`
	Owner          tasks.Owner      `json:"owner"`
	Reference      MCPTaskReference `json:"reference"`
	CreatedAt      time.Time        `json:"createdAt"`
	TTLMS          *int64           `json:"ttlMs"`
	PollIntervalMS int64            `json:"pollIntervalMs"`
}
type mcpTaskInputReceipt struct {
	RunID       string          `json:"runId"`
	InterruptID string          `json:"interruptId"`
	Response    json.RawMessage `json:"response"`
	Stage       string          `json:"stage"`
}
type mcpTaskPending struct {
	Inputs         map[string]mcpTaskInputReceipt `json:"inputs,omitempty"`
	Issued         map[string]string              `json:"issued,omitempty"`
	CancelStage    string                         `json:"cancelStage,omitempty"`
	ProjectionHash string                         `json:"projectionHash,omitempty"`
	UpdatedAt      time.Time                      `json:"updatedAt"`
}

// NewMCPTasksBackend stores only opaque aliases/input receipts in the existing
// AG-UI journal. The canonical native run remains the sole execution owner.
func NewMCPTasksBackend(store aguistore.Store, controls MCPTaskControls, options MCPTasksOptions) (*MCPTasksBackend, error) {
	if store == nil || controls.Authorize == nil || controls.Snapshot == nil || controls.Resume == nil || controls.Cancel == nil || options.Tenant == "" {
		return nil, fmt.Errorf("native task store, tenant and controls are required")
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.PollIntervalMS == 0 {
		options.PollIntervalMS = 1000
	}
	if options.PollIntervalMS < 0 || options.PollIntervalMS > 1<<53-1 || options.TTLMS != nil && (*options.TTLMS <= 0 || *options.TTLMS > 1<<53-1) {
		return nil, fmt.Errorf("invalid task retention/polling policy")
	}
	if options.TTLMS != nil {
		copy := *options.TTLMS
		options.TTLMS = &copy
	}
	return &MCPTasksBackend{store: store, controls: controls, options: options}, nil
}

// MCPTaskOrigin constructs private creation options from a trusted runtime
// reference. This object belongs to a server-side capture callback, not a caller.
func MCPTaskOrigin(reference MCPTaskReference) tasks.CreateOptions {
	raw, _ := json.Marshal(reference)
	return tasks.CreateOptions{Method: tasks.MethodCallTool, Origin: raw}
}
func (b *MCPTasksBackend) authenticate(ctx context.Context, owner tasks.Owner) error {
	if !owner.Valid() || owner.Tenant != b.options.Tenant || iauth.EffectiveUserID(ctx) != owner.Subject {
		return tasks.ErrNotFound
	}
	return nil
}
func (b *MCPTasksBackend) Create(ctx context.Context, owner tasks.Owner, caps schema.ClientCapabilities, options tasks.CreateOptions) (tasks.CreateTaskResult, error) {
	if !tasks.Supports(caps) {
		return tasks.CreateTaskResult{}, tasks.MissingCapability()
	}
	if options.Method != tasks.MethodCallTool {
		return tasks.CreateTaskResult{}, fmt.Errorf("only tools/call supports Tasks")
	}
	if err := b.authenticate(ctx, owner); err != nil {
		return tasks.CreateTaskResult{}, err
	}
	var reference MCPTaskReference
	decoder := json.NewDecoder(bytes.NewReader(options.Origin))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&reference) != nil || reference.ThreadID == "" || reference.RunID == "" {
		return tasks.CreateTaskResult{}, fmt.Errorf("trusted existing native run reference required")
	}
	if err := b.controls.Authorize(ctx, owner, reference); err != nil {
		return tasks.CreateTaskResult{}, tasks.ErrNotFound
	}
	// This read proves native admission already exists; creation never starts it.
	native, err := b.store.GetRun(ctx, owner.Subject, reference.ThreadID, reference.RunID)
	if err != nil || native == nil || native.Principal != owner.Subject || native.ThreadID != reference.ThreadID || native.RunID != reference.RunID {
		return tasks.CreateTaskResult{}, tasks.ErrNotFound
	}
	value := make([]byte, 24)
	if _, err = rand.Read(value); err != nil {
		return tasks.CreateTaskResult{}, err
	}
	id := "mcp-task-" + hex.EncodeToString(value)
	ttl := b.options.TTLMS
	if options.TTLMS != nil {
		ttl = options.TTLMS
	}
	if options.Unlimited {
		ttl = nil
	}
	if ttl != nil {
		copy := *ttl
		ttl = &copy
		if copy <= 0 || copy > 1<<53-1 {
			return tasks.CreateTaskResult{}, fmt.Errorf("invalid task TTL")
		}
	}
	alias := mcpTaskAlias{Version: "1", Owner: owner, Reference: reference, CreatedAt: b.options.Clock().UTC(), TTLMS: ttl, PollIntervalMS: b.options.PollIntervalMS}
	input := rawAGUI(map[string]any{"threadId": id, "runId": id, "messages": []any{}, "forwardedProps": map[string]any{mcpTaskAliasMarker: alias}})
	admitted, _, err := b.store.Admit(ctx, aguistore.Admission{Principal: owner.Subject, ThreadID: id, RunID: id, Input: input})
	if err != nil {
		return tasks.CreateTaskResult{}, err
	}
	// A protocol-only ledger has no native turn or worker. Its trusted marker
	// is explicitly excluded from AG-UI execution/recovery; task updates only
	// change its private receipts in the existing transaction/CAS store.
	// The private journal checkpoint is backend-owned provenance. Wire metadata
	// alone is insufficient to manufacture an executable task alias.
	event := rawAGUI(map[string]any{"type": "CUSTOM", "name": mcpTaskAliasMarker, "value": alias})
	_, err = b.store.Append(ctx, owner.Subject, id, id, admitted.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": id, "runId": id}), event}, &aguistore.Change{Pending: rawAGUI(mcpTaskPending{UpdatedAt: alias.CreatedAt})})
	if err != nil {
		return tasks.CreateTaskResult{}, err
	}
	result, err := b.Get(ctx, owner, id)
	if err != nil {
		return tasks.CreateTaskResult{}, err
	}
	for _, input := range result.InputRequests {
		if !tasks.SupportsInput(caps, input.Method) {
			return tasks.CreateTaskResult{}, &tasks.RPCError{Code: tasks.MissingCapabilityCode, Message: "Native task requires ordinary elicitation support", Data: rawAGUI(map[string]any{"requiredCapabilities": map[string]any{"elicitation": map[string]any{}}})}
		}
	}
	return tasks.CreateTaskResult{ResultType: tasks.ResultTask, DetailedTask: result.DetailedTask}, nil
}
func (b *MCPTasksBackend) load(ctx context.Context, owner tasks.Owner, id string) (*aguistore.Run, mcpTaskAlias, mcpTaskPending, error) {
	var alias mcpTaskAlias
	var pending mcpTaskPending
	if b.authenticate(ctx, owner) != nil || !strings.HasPrefix(id, "mcp-task-") || !tasks.ValidID(id) {
		return nil, alias, pending, tasks.ErrNotFound
	}
	run, err := b.store.GetRun(ctx, owner.Subject, id, id)
	if err != nil || run == nil || run.Principal != owner.Subject {
		return nil, alias, pending, tasks.ErrNotFound
	}
	checkpoint, err := b.store.Replay(ctx, owner.Subject, id, id, 1, 1)
	if err != nil || len(checkpoint) != 1 || checkpoint[0].Sequence != 2 {
		return nil, alias, pending, tasks.ErrNotFound
	}
	var event struct {
		Type, Name string
		Value      mcpTaskAlias
	}
	if json.Unmarshal(checkpoint[0].Event, &event) != nil || event.Type != "CUSTOM" || event.Name != mcpTaskAliasMarker || event.Value.Version != "1" || event.Value.Owner != owner {
		return nil, alias, pending, tasks.ErrNotFound
	}
	alias = event.Value
	if alias.TTLMS != nil && b.options.Clock().UnixMilli()-alias.CreatedAt.UnixMilli() >= *alias.TTLMS {
		return nil, alias, pending, tasks.ErrNotFound
	}
	if err = b.controls.Authorize(ctx, owner, alias.Reference); err != nil {
		return nil, alias, pending, tasks.ErrNotFound
	}
	if len(run.Pending) > 0 && json.Unmarshal(run.Pending, &pending) != nil {
		return nil, alias, pending, fmt.Errorf("invalid task alias receipts")
	}
	if pending.Inputs == nil {
		pending.Inputs = map[string]mcpTaskInputReceipt{}
	}
	if pending.Issued == nil {
		pending.Issued = map[string]string{}
	}
	return run, alias, pending, nil
}
func (b *MCPTasksBackend) current(ctx context.Context, owner tasks.Owner, reference MCPTaskReference) (MCPTaskReference, error) {
	seen := map[string]bool{}
	for round := 0; round < 32; round++ {
		if seen[reference.RunID] {
			return reference, fmt.Errorf("cyclic native continuation")
		}
		seen[reference.RunID] = true
		run, err := b.store.GetRun(ctx, owner.Subject, reference.ThreadID, reference.RunID)
		if err != nil {
			return reference, tasks.ErrNotFound
		}
		if run.Principal != owner.Subject || run.ThreadID != reference.ThreadID || run.RunID != reference.RunID {
			return reference, tasks.ErrNotFound
		}
		if run.ResumedByRunID == "" {
			return reference, nil
		}
		next, err := b.store.GetRun(ctx, owner.Subject, reference.ThreadID, run.ResumedByRunID)
		if err != nil || next.PriorRunID != run.RunID || next.TurnID != run.TurnID {
			return reference, fmt.Errorf("native continuation ownership/causal link changed")
		}
		reference.RunID = next.RunID
	}
	return reference, fmt.Errorf("native continuation exceeds bound")
}
func mcpTaskInputKey(id, run, interrupt string) string {
	sum := sha256.Sum256([]byte(id + "\x00" + run + "\x00" + interrupt))
	return hex.EncodeToString(sum[:])
}
func mcpTaskElicitation(interrupt agui.WireInterrupt) (tasks.InputRequest, error) {
	if interrupt.ID == "" || interrupt.ResponseSchema == nil || interrupt.Reason == "agently.client_tool" {
		return tasks.InputRequest{}, fmt.Errorf("native interrupt cannot be represented as MCP elicitation")
	}
	message := "Native execution requires input"
	if interrupt.Message != nil && *interrupt.Message != "" {
		message = *interrupt.Message
	}
	return tasks.InputRequest{Method: "elicitation/create", Params: rawAGUI(map[string]any{"mode": "form", "message": message, "requestedSchema": interrupt.ResponseSchema})}, nil
}
func (b *MCPTasksBackend) state(ctx context.Context, owner tasks.Owner, id string, alias mcpTaskAlias, pending *mcpTaskPending) (tasks.DetailedTask, MCPTaskReference, MCPTaskNativeSnapshot, error) {
	reference, err := b.current(ctx, owner, alias.Reference)
	if err != nil {
		return tasks.DetailedTask{}, reference, MCPTaskNativeSnapshot{}, err
	}
	if err = b.controls.Authorize(ctx, owner, reference); err != nil {
		return tasks.DetailedTask{}, reference, MCPTaskNativeSnapshot{}, tasks.ErrNotFound
	}
	snapshot, err := b.controls.Snapshot(ctx, owner, reference)
	if err != nil {
		return tasks.DetailedTask{}, reference, snapshot, err
	}
	interval := alias.PollIntervalMS
	state := tasks.DetailedTask{Task: tasks.Task{TaskID: id, Status: snapshot.Status, StatusMessage: snapshot.StatusMessage, CreatedAt: alias.CreatedAt, LastUpdatedAt: pending.UpdatedAt, TTLMS: alias.TTLMS, PollIntervalMS: &interval}, Result: snapshot.Result, Error: snapshot.Error}
	if state.LastUpdatedAt.IsZero() {
		state.LastUpdatedAt = alias.CreatedAt
	}
	if snapshot.UpdatedAt.After(state.LastUpdatedAt) {
		state.LastUpdatedAt = snapshot.UpdatedAt
	}
	if state.Status == tasks.InputRequired {
		state.InputRequests = tasks.InputRequests{}
		for _, interrupt := range snapshot.Interrupts {
			request, err := mcpTaskElicitation(interrupt)
			if err != nil {
				return tasks.DetailedTask{}, reference, snapshot, err
			}
			key := mcpTaskInputKey(id, reference.RunID, interrupt.ID)
			raw, _ := json.Marshal(request)
			digest := sha256.Sum256(raw)
			fingerprint := hex.EncodeToString(digest[:])
			if issued, exists := pending.Issued[key]; exists && issued != fingerprint {
				return tasks.DetailedTask{}, reference, snapshot, fmt.Errorf("native input key payload changed")
			}
			pending.Issued[key] = fingerprint
			if _, accepted := pending.Inputs[key]; !accepted {
				state.InputRequests[key] = request
			}
		}
		if len(state.InputRequests) == 0 {
			state.Status = tasks.Working
			state.InputRequests = nil
		}
	}
	if err = state.Validate(); err != nil {
		return tasks.DetailedTask{}, reference, snapshot, err
	}
	return state, reference, snapshot, nil
}
func (b *MCPTasksBackend) Get(ctx context.Context, owner tasks.Owner, id string) (tasks.GetTaskResult, error) {
	for attempt := 0; attempt < 8; attempt++ {
		run, alias, pending, err := b.load(ctx, owner, id)
		if err != nil {
			return tasks.GetTaskResult{}, err
		}
		state, _, _, err := b.state(ctx, owner, id, alias, &pending)
		if err != nil {
			return tasks.GetTaskResult{}, err
		}
		raw, _ := json.Marshal(struct {
			Status  tasks.Status
			Message string
			Inputs  tasks.InputRequests
			Result  json.RawMessage
			Error   *tasks.RPCError
		}{state.Status, state.StatusMessage, state.InputRequests, state.Result, state.Error})
		digest := sha256.Sum256(raw)
		hash := hex.EncodeToString(digest[:])
		if pending.ProjectionHash != hash {
			pending.ProjectionHash = hash
			pending.UpdatedAt = b.options.Clock().UTC()
			if pending.UpdatedAt.Before(state.LastUpdatedAt) {
				pending.UpdatedAt = state.LastUpdatedAt
			}
			state.LastUpdatedAt = pending.UpdatedAt
			_, err = b.store.Append(ctx, owner.Subject, id, id, run.Revision, nil, &aguistore.Change{Pending: rawAGUI(pending)})
			if errors.Is(err, aguistore.ErrConflict) {
				continue
			}
			if err != nil {
				return tasks.GetTaskResult{}, err
			}
		}
		return tasks.GetTaskResult{ResultType: tasks.ResultComplete, DetailedTask: state}, nil
	}
	return tasks.GetTaskResult{}, tasks.ErrConflict
}
func taskInputAnswer(interrupt agui.WireInterrupt, response json.RawMessage) (agui.WireResumeEntry, error) {
	var elicited struct {
		Action  string          `json:"action"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(response, &elicited) != nil {
		return agui.WireResumeEntry{}, fmt.Errorf("invalid elicitation response")
	}
	entry := agui.WireResumeEntry{InterruptId: interrupt.ID}
	switch elicited.Action {
	case "accept":
		if !tasks.ValidObject(elicited.Content) {
			return entry, fmt.Errorf("accepted elicitation requires object content")
		}
		entry.Status = "resolved"
		content := append(json.RawMessage(nil), elicited.Content...)
		entry.Payload = &content
	case "decline", "cancel":
		entry.Status = "cancelled"
	default:
		return entry, fmt.Errorf("invalid elicitation action")
	}
	// The ordinary native resume preflight applies the authoritative schema and
	// approval policy. Accepting an elicitation is never itself tool approval.
	return entry, nil
}
func (b *MCPTasksBackend) Update(ctx context.Context, owner tasks.Owner, id string, responses tasks.InputResponses) (tasks.Acknowledgement, error) {
	if tasks.ValidateResponses(responses) != nil {
		return tasks.Acknowledgement{}, &tasks.RPCError{Code: -32602, Message: "invalid inputResponses"}
	}
	for attempt := 0; attempt < 8; attempt++ {
		run, alias, pending, err := b.load(ctx, owner, id)
		if err != nil {
			return tasks.Acknowledgement{}, err
		}
		_, reference, snapshot, err := b.state(ctx, owner, id, alias, &pending)
		if err != nil {
			return tasks.Acknowledgement{}, err
		}
		if snapshot.Status != tasks.InputRequired {
			return tasks.Acknowledgement{ResultType: tasks.ResultComplete}, nil
		}
		entries := []agui.WireResumeEntry{}
		ready := true
		changed := false
		dispatching := false
		for _, interrupt := range snapshot.Interrupts {
			key := mcpTaskInputKey(id, reference.RunID, interrupt.ID)
			receipt, accepted := pending.Inputs[key]
			if !accepted {
				if response, exists := responses[key]; exists {
					if _, err = taskInputAnswer(interrupt, response); err != nil {
						return tasks.Acknowledgement{}, &tasks.RPCError{Code: -32602, Message: "invalid native elicitation response"}
					}
					receipt = mcpTaskInputReceipt{RunID: reference.RunID, InterruptID: interrupt.ID, Response: append(json.RawMessage(nil), response...), Stage: "prepared"}
					pending.Inputs[key] = receipt
					accepted = true
					changed = true
				}
			}
			if !accepted {
				ready = false
				continue
			}
			if receipt.Stage == "dispatching" || receipt.Stage == "uncertain" {
				dispatching = true
			}
			entry, err := taskInputAnswer(interrupt, receipt.Response)
			if err != nil {
				return tasks.Acknowledgement{}, err
			}
			entries = append(entries, entry)
		}
		if dispatching {
			return tasks.Acknowledgement{}, fmt.Errorf("native input dispatch uncertain; original execution will not be repeated")
		}
		if ready && len(entries) > 0 {
			for key, receipt := range pending.Inputs {
				if receipt.RunID == reference.RunID {
					receipt.Stage = "dispatching"
					pending.Inputs[key] = receipt
				}
			}
			changed = true
		}
		if !changed {
			return tasks.Acknowledgement{ResultType: tasks.ResultComplete}, nil
		}
		pending.UpdatedAt = b.options.Clock().UTC()
		_, err = b.store.Append(ctx, owner.Subject, id, id, run.Revision, nil, &aguistore.Change{Pending: rawAGUI(pending)})
		if errors.Is(err, aguistore.ErrConflict) {
			continue
		}
		if err != nil {
			return tasks.Acknowledgement{}, err
		}
		if !ready {
			return tasks.Acknowledgement{ResultType: tasks.ResultComplete}, nil
		}
		// Detached/native work has its own lifetime. Only the accepted control call
		// runs here; an HTTP disconnect must not cancel the existing execution.
		err = b.controls.Resume(context.WithoutCancel(ctx), owner, reference, entries)
		if err != nil {
			return tasks.Acknowledgement{}, err
		}
		return tasks.Acknowledgement{ResultType: tasks.ResultComplete}, nil
	}
	return tasks.Acknowledgement{}, tasks.ErrConflict
}
func (b *MCPTasksBackend) Cancel(ctx context.Context, owner tasks.Owner, id string) (tasks.Acknowledgement, error) {
	for attempt := 0; attempt < 8; attempt++ {
		run, alias, pending, err := b.load(ctx, owner, id)
		if err != nil {
			return tasks.Acknowledgement{}, err
		}
		reference, err := b.current(ctx, owner, alias.Reference)
		if err != nil {
			return tasks.Acknowledgement{}, err
		}
		if err = b.controls.Authorize(ctx, owner, reference); err != nil {
			return tasks.Acknowledgement{}, tasks.ErrNotFound
		}
		if pending.CancelStage != "" {
			return tasks.Acknowledgement{ResultType: tasks.ResultComplete}, nil
		}
		pending.CancelStage = "dispatching"
		// A private dispatch receipt does not change the canonical task state.
		// Get advances UpdatedAt when the authoritative native projection changes.
		_, err = b.store.Append(ctx, owner.Subject, id, id, run.Revision, nil, &aguistore.Change{Pending: rawAGUI(pending)})
		if errors.Is(err, aguistore.ErrConflict) {
			continue
		}
		if err != nil {
			return tasks.Acknowledgement{}, err
		}
		if err = b.controls.Cancel(context.WithoutCancel(ctx), owner, reference); err != nil {
			return tasks.Acknowledgement{}, err
		}
		return tasks.Acknowledgement{ResultType: tasks.ResultComplete}, nil
	}
	return tasks.Acknowledgement{}, tasks.ErrConflict
}

// MCPTasksAGUISnapshot projects existing journals only. It never starts a run,
// executes a tool, recovers a worker or fabricates a result from a queue receipt.
func MCPTasksAGUISnapshot(store aguistore.Store) func(context.Context, tasks.Owner, MCPTaskReference) (MCPTaskNativeSnapshot, error) {
	return func(ctx context.Context, owner tasks.Owner, reference MCPTaskReference) (MCPTaskNativeSnapshot, error) {
		run, err := store.GetRun(ctx, owner.Subject, reference.ThreadID, reference.RunID)
		if err != nil {
			return MCPTaskNativeSnapshot{}, err
		}
		result := MCPTaskNativeSnapshot{Status: tasks.Working}
		switch run.Status {
		case aguistore.StatusCancelled:
			result.Status = tasks.Cancelled
		case aguistore.StatusInterrupted:
			var pending struct {
				Interrupts []agui.WireInterrupt `json:"interrupts"`
			}
			if json.Unmarshal(run.Pending, &pending) != nil {
				return result, fmt.Errorf("native pending inputs unavailable")
			}
			if len(pending.Interrupts) > 0 {
				result.Status = tasks.InputRequired
				result.Interrupts = pending.Interrupts
			}
		}
		if run.Status != aguistore.StatusFinished && run.Status != aguistore.StatusError {
			return result, nil
		}
		after := int64(0)
		for after < run.LastSequence {
			page, err := store.Replay(ctx, owner.Subject, run.ThreadID, run.RunID, after, 256)
			if err != nil || len(page) == 0 {
				return result, fmt.Errorf("native task outcome journal unavailable")
			}
			for _, event := range page {
				if event.Sequence != after+1 {
					return result, fmt.Errorf("native task outcome journal has a gap")
				}
				after = event.Sequence
				var wire struct {
					Type    string          `json:"type"`
					Result  json.RawMessage `json:"result"`
					Code    json.RawMessage `json:"code"`
					Message string          `json:"message"`
				}
				if json.Unmarshal(event.Event, &wire) != nil {
					return result, fmt.Errorf("invalid native outcome")
				}
				if wire.Type == "RUN_FINISHED" && tasks.ValidObject(wire.Result) {
					var shape map[string]json.RawMessage
					_ = json.Unmarshal(wire.Result, &shape)
					if _, exists := shape["content"]; exists {
						result.Status = tasks.Completed
						result.Result = append(json.RawMessage(nil), wire.Result...)
					}
				}
				if wire.Type == "RUN_ERROR" {
					// A native execution error is not an original MCP result or JSON-RPC
					// error. Hosts with another canonical outcome store must project it
					// explicitly; do not fabricate a successful task/tool-error payload.
					return result, fmt.Errorf("canonical MCP task error/result is unavailable")
				}
			}
		}
		if result.Status != tasks.Completed && result.Status != tasks.Failed {
			return result, fmt.Errorf("canonical MCP task result is unavailable")
		}
		return result, nil
	}
}

var _ tasks.Backend = (*MCPTasksBackend)(nil)

// MCPTasksAGUIControls binds task controls to existing AG-UI admission/resume,
// owned cancellation and journal replay. Authorize must also enforce the current
// exported-tool policy and exact native causal reference; ownership alone is
// not a grant to expose host-only or administrative tool results.
func MCPTasksAGUIControls(client Client, store aguistore.Store, authorize func(context.Context, tasks.Owner, MCPTaskReference) error) MCPTaskControls {
	return MCPTaskControls{Authorize: authorize, Snapshot: MCPTasksAGUISnapshot(store), Watch: mcpTasksAGUIWatch(client),
		Resume: func(ctx context.Context, owner tasks.Owner, reference MCPTaskReference, answers []agui.WireResumeEntry) error {
			run, err := store.GetRun(ctx, owner.Subject, reference.ThreadID, reference.RunID)
			if err != nil {
				return err
			}
			var input agui.RunAgentInput
			if json.Unmarshal(run.Input, &input) != nil {
				return fmt.Errorf("native input unavailable")
			}
			input.RunID = uuid.NewString()
			input.ParentRunID = run.RunID
			input.Resume = rawAGUI(answers)
			raw := rawAGUI(input)
			request, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodPost, "/v1/ag-ui/run", bytes.NewReader(raw))
			if err != nil {
				return err
			}
			request.Header.Set("Content-Type", "application/json")
			writer := newMCPTaskResumeWriter()
			go func() { defer close(writer.finished); handleAGUIRun(client, nil)(writer, request) }()
			// Wait only for durable native admission, not completion. The existing
			// native worker owns execution and survives observer/request disconnect.
			timeout := time.NewTimer(10 * time.Second)
			defer timeout.Stop()
			select {
			case <-writer.admitted:
				admitted, err := store.GetRun(ctx, owner.Subject, input.ThreadID, input.RunID)
				if err != nil || admitted.PriorRunID != run.RunID || admitted.TurnID != run.TurnID {
					return fmt.Errorf("native resume admission is unconfirmed")
				}
				return nil
			case <-writer.finished:
				admitted, err := store.GetRun(ctx, owner.Subject, input.ThreadID, input.RunID)
				if err != nil || admitted.PriorRunID != run.RunID {
					return fmt.Errorf("native resume was not admitted")
				}
				return nil
			case <-timeout.C:
				return fmt.Errorf("native resume admission unknown; original input will not be replayed")
			}
		},
		Cancel: func(ctx context.Context, owner tasks.Owner, reference MCPTaskReference) error {
			_, err := cancelAGUIRun(ctx, client, store, owner.Subject, reference.ThreadID, reference.RunID)
			return err
		},
	}
}

type mcpTaskResumeWriter struct {
	headers  http.Header
	admitted chan struct{}
	finished chan struct{}
	once     sync.Once
	buffer   string
}

func newMCPTaskResumeWriter() *mcpTaskResumeWriter {
	return &mcpTaskResumeWriter{headers: http.Header{}, admitted: make(chan struct{}), finished: make(chan struct{})}
}
func (w *mcpTaskResumeWriter) Header() http.Header { return w.headers }
func (*mcpTaskResumeWriter) WriteHeader(int)       {}
func (*mcpTaskResumeWriter) Flush()                {}
func (w *mcpTaskResumeWriter) Write(raw []byte) (int, error) {
	w.buffer += string(raw)
	if strings.Contains(w.buffer, `"type":"RUN_STARTED"`) {
		w.once.Do(func() { close(w.admitted) })
	}
	if len(w.buffer) > 65536 {
		w.buffer = w.buffer[len(w.buffer)-4096:]
	}
	return len(raw), nil
}

// isMCPTaskLedgerRun is intentionally conservative for reserved task IDs. No
// externally supplied forwardedProps can turn a ledger into an execution.
func isMCPTaskLedgerRun(record *aguistore.Run) bool {
	return record != nil && strings.HasPrefix(record.ThreadID, "mcp-task-") && record.RunID == record.ThreadID && record.TurnID == ""
}
