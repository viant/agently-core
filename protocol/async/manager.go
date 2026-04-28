package async

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type State string

const (
	StateStarted   State = "started"
	StateRunning   State = "running"
	StateWaiting   State = "waiting"
	StateCompleted State = "completed"
	StateFailed    State = "failed"
	StateCanceled  State = "canceled"
)

const (
	DefaultPercentSignalThreshold = 5
	DefaultMaxPollFailures        = 3
	DefaultIdleTimeoutMs          = int((10 * time.Minute) / time.Millisecond)
)

type OperationRecord struct {
	ID                   string
	ParentConvID         string
	ParentTurnID         string
	ToolCallID           string
	ToolMessageID        string
	ToolName             string
	StatusToolName       string
	StatusOperationIDArg string
	SameToolRecall       bool
	StatusArgs           map[string]interface{}
	CancelToolName       string
	RequestArgsDigest    string
	RequestArgs          map[string]interface{}
	OperationIntent      string
	OperationSummary     string
	ExecutionMode        string
	State                State
	Status               string
	Message              string
	MessageKind          string
	Percent              *int
	LastSignaledPercent  *int
	KeyData              json.RawMessage
	Error                string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	LastPayloadChangeAt  time.Time
	TimeoutAt            *time.Time
	TimeoutMs            int
	IdleTimeoutMs        int
	PollIntervalMs       int
	PollFailures         int
	changeDigest         string
	pendingChange        bool
}

func (r *OperationRecord) Terminal() bool {
	if r == nil {
		return true
	}
	switch r.State {
	case StateCompleted, StateFailed, StateCanceled:
		return true
	default:
		return false
	}
}

type RegisterInput struct {
	ID                   string
	ParentConvID         string
	ParentTurnID         string
	ToolCallID           string
	ToolMessageID        string
	ToolName             string
	StatusToolName       string
	StatusOperationIDArg string
	SameToolRecall       bool
	StatusArgs           map[string]interface{}
	CancelToolName       string
	RequestArgsDigest    string
	RequestArgs          map[string]interface{}
	OperationIntent      string
	OperationSummary     string
	ExecutionMode        string
	Status               string
	Message              string
	MessageKind          string
	Percent              *int
	KeyData              json.RawMessage
	Error                string
	TimeoutMs            int
	IdleTimeoutMs        int
	PollIntervalMs       int
}

type ChangeEvent struct {
	OperationID  string          `json:"operationId,omitempty"`
	PriorDigest  string          `json:"priorDigest,omitempty"`
	NewDigest    string          `json:"newDigest,omitempty"`
	Status       string          `json:"status,omitempty"`
	Message      string          `json:"message,omitempty"`
	MessageKind  string          `json:"messageKind,omitempty"`
	Percent      *int            `json:"percent,omitempty"`
	KeyData      json.RawMessage `json:"keyData,omitempty"`
	Error        string          `json:"error,omitempty"`
	State        State           `json:"state,omitempty"`
	ChangedAt    time.Time       `json:"changedAt,omitempty"`
	ToolName     string          `json:"toolName,omitempty"`
	Intent       string          `json:"intent,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	Conversation string          `json:"conversationId,omitempty"`
	TurnID       string          `json:"turnId,omitempty"`
}

type AggregatedItem struct {
	OperationID      string          `json:"operationId,omitempty"`
	ToolName         string          `json:"toolName,omitempty"`
	OperationIntent  string          `json:"operationIntent,omitempty"`
	OperationSummary string          `json:"operationSummary,omitempty"`
	State            State           `json:"state,omitempty"`
	Reason           string          `json:"reason,omitempty"`
	Detail           string          `json:"detail,omitempty"`
	Payload          json.RawMessage `json:"payload,omitempty"`
}

type AggregatedResult struct {
	Items          []AggregatedItem `json:"items,omitempty"`
	OpsStillActive bool             `json:"opsStillActive,omitempty"`
}

// Filter is the generic selector for listing operations. Zero-value fields
// are treated as "don't filter by this."
//
// INTERNAL USE ONLY. This struct is not part of any LLM-facing tool schema.
// In particular, `ConversationID` is always populated by the runtime from
// the request context — never from LLM-provided input. Exposing it to the
// LLM would let a prompt-injected or hallucinated conversation id leak
// ops across conversations; every path that builds a Filter must read
// ConversationID from trusted context, not from tool args.
//
// Tool-agnostic: an op from any source (internal service, MCP, system,
// user tool) shows up in the same shape. Callers that legitimately want
// cross-conversation visibility (tests, admin flows) pass ConversationID
// = "".
type Filter struct {
	ConversationID string // populated by runtime from context; never by LLM
	Tool           string // start-tool name, e.g. "llm/agents:start"
	ExecutionMode  string // "wait" | "detach"; "" = any
}

// PendingOp is a compact, LLM-oriented view of a non-terminal operation.
// Contains exactly what a caller needs to invoke the status tool.
//
// Primary usage pattern: the LLM reads `statusTool` and sends
// `statusArgs` verbatim to it. `statusArgs` is always populated with a
// ready-to-send map — it includes the op id under the correct arg name
// plus any `StatusConfig.ExtraArgs` the tool expects. The LLM does not
// have to know or reconstruct extras.
//
// `operationIdArg` and `sameToolRecall` are exposed for introspection
// (logging, debugging, LLM transparency) but are not required for a
// correct status call — `statusArgs` alone is sufficient.
type PendingOp struct {
	OperationID    string `json:"operationId"`
	Tool           string `json:"tool"`
	StatusTool     string `json:"statusTool,omitempty"`
	OperationIDArg string `json:"operationIdArg,omitempty"`
	SameToolRecall bool   `json:"sameToolRecall,omitempty"`
	// StatusArgs is the ready-to-send argument map for StatusTool.
	// Includes the op id under OperationIDArg plus any declared
	// StatusConfig.ExtraArgs (and, for same-tool-recall patterns, any
	// additional args needed to elicit status from the run tool).
	StatusArgs    map[string]interface{} `json:"statusArgs,omitempty"`
	ExecutionMode string                 `json:"executionMode,omitempty"`
	State         State                  `json:"state,omitempty"`
	Intent        string                 `json:"intent,omitempty"`
	Summary       string                 `json:"summary,omitempty"`
	UpdatedAt     time.Time              `json:"updatedAt,omitempty"`
}

type changeSubscription struct {
	targets map[string]struct{}
	ch      chan ChangeEvent
}

type UpdateInput struct {
	ID          string
	Status      string
	Message     string
	MessageKind string
	Percent     *int
	KeyData     json.RawMessage
	Error       string
	State       State
}

type Manager struct {
	mu            sync.Mutex
	ops           map[string]*OperationRecord
	pollers       map[string]struct{}
	pollerCancels map[string]context.CancelFunc
	waiters       map[string][]chan struct{}
	subscriptions map[uint64]*changeSubscription
	nextSubID     uint64

	// gcCancels tracks the cancel function of every goroutine spawned by
	// StartGC. Close() fires these so the Manager owns the teardown of
	// the sweeper goroutines it launched rather than leaving them
	// dangling on a separate ctx.
	gcCancels []context.CancelFunc
	// gcDone receives from every GC goroutine as it exits, so Close can
	// block until all sweepers have returned (no leaks past Close).
	gcDone []chan struct{}
	// pollerWG counts the poller goroutines admitted via AdmitPoller /
	// TryStartPoller. Each admission does wg.Add(1); FinishPoller does
	// wg.Done exactly once per admission (guarded by "was entry present
	// in m.pollers"). Close() waits on this after firing all cancels so
	// poller goroutines have fully exited before Close returns — no
	// leaked goroutines past shutdown.
	pollerWG sync.WaitGroup
	// closed guards Close idempotency and short-circuits new StartGC /
	// AdmitPoller / TryStartPoller calls after shutdown so a post-Close
	// scheduler cannot resurrect background work.
	closed bool

	// Counters — atomic so Stats() can read without taking mu. Lifetime
	// counts; never reset. Small numeric surface for operator debugging.
	registerCount          atomic.Int64
	registerOverwriteCount atomic.Int64
	updateCount            atomic.Int64
	updateChangedCount     atomic.Int64
	sweepPruneCount        atomic.Int64
	subscribeCount         atomic.Int64
	unsubscribeCount       atomic.Int64
}

// ManagerStats is a read-only snapshot of Manager counters plus a live
// gauge of in-flight state. Safe to call concurrently with any other
// Manager operation. Counters are lifetime-cumulative from NewManager;
// gauges reflect state at call time.
type ManagerStats struct {
	// Lifetime counters.
	RegisterCount          int64 `json:"registerCount"`
	RegisterOverwriteCount int64 `json:"registerOverwriteCount"`
	UpdateCount            int64 `json:"updateCount"`
	UpdateChangedCount     int64 `json:"updateChangedCount"`
	SweepPruneCount        int64 `json:"sweepPruneCount"`
	SubscribeCount         int64 `json:"subscribeCount"`
	UnsubscribeCount       int64 `json:"unsubscribeCount"`

	// Live gauges at call time.
	ActiveOps           int `json:"activeOps"`
	ActiveSubscriptions int `json:"activeSubscriptions"`
	ActivePollers       int `json:"activePollers"`
}

// Stats returns a snapshot of lifetime counters and live gauges. Intended
// for operator-facing debug surfaces (expvar, admin endpoints) — not on
// any correctness path.
func (m *Manager) Stats() ManagerStats {
	out := ManagerStats{
		RegisterCount:          m.registerCount.Load(),
		RegisterOverwriteCount: m.registerOverwriteCount.Load(),
		UpdateCount:            m.updateCount.Load(),
		UpdateChangedCount:     m.updateChangedCount.Load(),
		SweepPruneCount:        m.sweepPruneCount.Load(),
		SubscribeCount:         m.subscribeCount.Load(),
		UnsubscribeCount:       m.unsubscribeCount.Load(),
	}
	m.mu.Lock()
	out.ActiveOps = len(m.ops)
	out.ActiveSubscriptions = len(m.subscriptions)
	out.ActivePollers = len(m.pollers)
	m.mu.Unlock()
	return out
}

// deepCloneValue returns a deep copy of v, recursively cloning any
// nested map[string]interface{} and []interface{}. Primitive leaves
// (string, numeric, bool, nil, json.RawMessage) are returned by value
// or via their existing clone primitive. Other reference types are
// returned as-is — their shape is not known to this package.
//
// Used by paths that hand map payloads out to consumers (ListOperations,
// cloneRecord) so a consumer cannot mutate the Manager's canonical copy
// via a shared nested reference.
func deepCloneValue(v interface{}) interface{} {
	switch actual := v.(type) {
	case nil:
		return nil
	case map[string]interface{}:
		return deepCloneMap(actual)
	case []interface{}:
		out := make([]interface{}, len(actual))
		for i, item := range actual {
			out[i] = deepCloneValue(item)
		}
		return out
	case json.RawMessage:
		return cloneJSON(actual)
	default:
		// Primitives (string, int, int64, float64, bool) are returned as
		// value. Unknown reference types (struct pointers, typed slices)
		// are returned as-is; callers that need those deep-cloned should
		// extend this helper.
		return actual
	}
}

func deepCloneMap(input map[string]interface{}) map[string]interface{} {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(input))
	for key, value := range input {
		out[key] = deepCloneValue(value)
	}
	return out
}

func (m *Manager) BindToolCarrier(_ context.Context, id, toolCallID, toolMessageID, toolName string) (*OperationRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.ops[strings.TrimSpace(id)]
	if rec == nil {
		return nil, false
	}
	changed := false
	if v := strings.TrimSpace(toolCallID); v != "" && v != rec.ToolCallID {
		rec.ToolCallID = v
		changed = true
	}
	if v := strings.TrimSpace(toolMessageID); v != "" && v != rec.ToolMessageID {
		rec.ToolMessageID = v
		changed = true
	}
	if v := strings.TrimSpace(toolName); v != "" && v != rec.ToolName {
		rec.ToolName = v
		changed = true
	}
	return cloneRecord(rec), changed
}

func NewManager() *Manager {
	return &Manager{
		ops:           map[string]*OperationRecord{},
		pollers:       map[string]struct{}{},
		pollerCancels: map[string]context.CancelFunc{},
		waiters:       map[string][]chan struct{}{},
		subscriptions: map[uint64]*changeSubscription{},
	}
}

// AdmitPoller atomically admits a poller goroutine: refuses if the
// Manager is closed or a poller for the same op is already running,
// otherwise adds the map entry, stores the cancel func, and bumps
// pollerWG. Callers wrap a poller launch with:
//
//	pollCtx, cancel := context.WithCancel(...)
//	if !manager.AdmitPoller(ctx, opID, cancel) {
//	    cancel()
//	    return
//	}
//	go func() {
//	    defer manager.FinishPoller(ctx, opID) // always runs for an admitted poller
//	    defer cancel()
//	    // ... poll loop ...
//	}()
//
// Storing the cancel in the same critical section as admission closes
// the race where TryStartPoller + StorePollerCancel were two separate
// calls — a concurrent Close firing between them would have left the
// admitted poller with no registered cancel.
func (m *Manager) AdmitPoller(_ context.Context, id string, cancel context.CancelFunc) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	if m.pollers == nil {
		m.pollers = map[string]struct{}{}
	}
	if _, exists := m.pollers[id]; exists {
		return false
	}
	m.pollers[id] = struct{}{}
	if cancel != nil {
		if m.pollerCancels == nil {
			m.pollerCancels = map[string]context.CancelFunc{}
		}
		m.pollerCancels[id] = cancel
	}
	m.pollerWG.Add(1)
	return true
}

// TryStartPoller is the legacy admission API: admits a poller without
// storing a cancel func. Prefer AdmitPoller in new code. Retained for
// tests and callers that register the cancel via StorePollerCancel
// separately. Refuses admission after Close.
func (m *Manager) TryStartPoller(_ context.Context, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	if m.pollers == nil {
		m.pollers = map[string]struct{}{}
	}
	if _, exists := m.pollers[id]; exists {
		return false
	}
	m.pollers[id] = struct{}{}
	m.pollerWG.Add(1)
	return true
}

// FinishPoller deregisters a poller. It is idempotent — only the first
// call (the one that actually removed the map entry) decrements
// pollerWG. This matters because dropOpLocked no longer touches
// m.pollers; the admitted goroutine's FinishPoller is the sole
// deregistration site, so the wg stays tied to goroutine lifecycle.
//
// Callers MUST call FinishPoller exactly once per successful admission,
// typically via `defer manager.FinishPoller(ctx, opID)` placed BEFORE
// any early return in the poller goroutine (so partial setup doesn't
// leak the admission).
func (m *Manager) FinishPoller(_ context.Context, id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	m.mu.Lock()
	wasRegistered := false
	if m.pollers != nil {
		if _, ok := m.pollers[id]; ok {
			delete(m.pollers, id)
			wasRegistered = true
		}
	}
	// Clean up the stored cancel function; calling it here is intentionally
	// safe (context.CancelFunc is idempotent).
	if m.pollerCancels != nil {
		if cancel, ok := m.pollerCancels[id]; ok {
			cancel()
			delete(m.pollerCancels, id)
		}
	}
	m.mu.Unlock()
	if wasRegistered {
		m.pollerWG.Done()
	}
}

// StorePollerCancel registers a cancel function for the given operation
// so that CancelTurnPollers / Close can reach it.
//
// Deprecated: use AdmitPoller, which stores the cancel atomically with
// admission. StorePollerCancel remains for tests and legacy callers that
// paired it with TryStartPoller — they should migrate when convenient.
func (m *Manager) StorePollerCancel(_ context.Context, id string, cancel context.CancelFunc) {
	if cancel == nil || strings.TrimSpace(id) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pollerCancels == nil {
		m.pollerCancels = map[string]context.CancelFunc{}
	}
	m.pollerCancels[strings.TrimSpace(id)] = cancel
}

// CancelTurnPollers cancels every autonomous poller that belongs to the given
// conversation/turn. It is safe to call when no pollers are running (no-op).
// The service layer calls this when a turn is explicitly canceled so pollers do
// not outlive the turn they belong to.
func (m *Manager) CancelTurnPollers(_ context.Context, convID, turnID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := turnKey(convID, turnID)
	for _, rec := range m.ops {
		if turnKey(rec.ParentConvID, rec.ParentTurnID) != key {
			continue
		}
		if m.pollerCancels == nil {
			continue
		}
		if cancel, ok := m.pollerCancels[rec.ID]; ok {
			cancel()
		}
	}
}

// Register creates a new op record. Returns the canonical record and a
// flag indicating whether a prior record under the same id was
// overwritten. Overwrite is almost always a bug — a buggy
// OperationIDPath or a retried start-handler that double-registers —
// so it is logged at warn and counted in Stats(). The previous record's
// state is lost.
func (m *Manager) Register(_ context.Context, input RegisterInput) (*OperationRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := strings.TrimSpace(input.ID)
	_, existed := m.ops[id]
	if existed {
		log.Printf("[async/manager] WARN: Register overwriting existing op id=%q conv=%q turn=%q tool=%q — previous state lost",
			id,
			strings.TrimSpace(input.ParentConvID),
			strings.TrimSpace(input.ParentTurnID),
			strings.TrimSpace(input.ToolName))
		m.registerOverwriteCount.Add(1)
	}
	m.registerCount.Add(1)
	now := time.Now()
	rec := &OperationRecord{
		ID:                   id,
		ParentConvID:         strings.TrimSpace(input.ParentConvID),
		ParentTurnID:         strings.TrimSpace(input.ParentTurnID),
		ToolCallID:           strings.TrimSpace(input.ToolCallID),
		ToolMessageID:        strings.TrimSpace(input.ToolMessageID),
		ToolName:             strings.TrimSpace(input.ToolName),
		StatusToolName:       strings.TrimSpace(input.StatusToolName),
		StatusOperationIDArg: strings.TrimSpace(input.StatusOperationIDArg),
		SameToolRecall:       input.SameToolRecall,
		StatusArgs:           deepCloneMap(input.StatusArgs),
		CancelToolName:       strings.TrimSpace(input.CancelToolName),
		RequestArgsDigest:    strings.TrimSpace(input.RequestArgsDigest),
		RequestArgs:          deepCloneMap(input.RequestArgs),
		OperationIntent:      strings.TrimSpace(input.OperationIntent),
		OperationSummary:     strings.TrimSpace(input.OperationSummary),
		ExecutionMode:        NormalizeExecutionMode(input.ExecutionMode, string(ExecutionModeWait)),
		Status:               strings.TrimSpace(input.Status),
		Message:              strings.TrimSpace(input.Message),
		MessageKind:          strings.TrimSpace(input.MessageKind),
		Percent:              cloneIntPtr(input.Percent),
		LastSignaledPercent:  cloneIntPtr(input.Percent),
		KeyData:              cloneJSON(input.KeyData),
		Error:                strings.TrimSpace(input.Error),
		CreatedAt:            now,
		UpdatedAt:            now,
		LastPayloadChangeAt:  now,
		TimeoutMs:            input.TimeoutMs,
		IdleTimeoutMs:        input.IdleTimeoutMs,
		PollIntervalMs:       input.PollIntervalMs,
		pendingChange:        true,
	}
	// TimeoutAt is a wall-clock ceiling used by both the wait-mode poller
	// loop (`PollAsyncOperation.handleTimeoutIfExpired`) AND the activated
	// status-tool loop in `tool_executor.maybeExecuteActivatedStatusTool`
	// (detach mode). The two paths use the same field with slightly
	// different intents — wait mode treats it as "the op has timed out",
	// detach mode treats it as "the activated-status poll should stop
	// re-calling" — but both are sourced from the same RegisterInput.
	// Therefore TimeoutMs > 0 always sets TimeoutAt regardless of mode.
	if input.TimeoutMs > 0 {
		timeoutAt := now.Add(time.Duration(input.TimeoutMs) * time.Millisecond)
		rec.TimeoutAt = &timeoutAt
	}
	if rec.IdleTimeoutMs <= 0 {
		rec.IdleTimeoutMs = DefaultIdleTimeoutMs
	}
	rec.State = DeriveState(rec.Status, input.Error, "")
	rec.changeDigest = changeDigest(rec.Status, rec.Message, rec.State, rec.KeyData)
	m.ops[rec.ID] = rec
	m.signalLocked(turnKey(rec.ParentConvID, rec.ParentTurnID))
	return rec, existed
}

func (m *Manager) Get(_ context.Context, id string) (*OperationRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.ops[strings.TrimSpace(id)]
	if rec == nil {
		return nil, false
	}
	return cloneRecord(rec), true
}

func (m *Manager) Update(_ context.Context, input UpdateInput) (*OperationRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.ops[strings.TrimSpace(input.ID)]
	if rec == nil {
		return nil, false
	}
	m.updateCount.Add(1)
	changed := false
	if status := strings.TrimSpace(input.Status); status != "" && status != rec.Status {
		rec.Status = status
		changed = true
	}
	if msg := strings.TrimSpace(input.Message); msg != rec.Message {
		rec.Message = msg
		changed = true
	}
	if kind := strings.TrimSpace(input.MessageKind); kind != rec.MessageKind {
		rec.MessageKind = kind
		changed = true
	}
	percentChanged := !equalIntPtr(rec.Percent, input.Percent)
	if percentChanged {
		rec.Percent = cloneIntPtr(input.Percent)
		changed = true
	}
	keyDataChanged := len(input.KeyData) > 0 && !jsonEqual(rec.KeyData, input.KeyData)
	if keyDataChanged {
		rec.KeyData = cloneJSON(input.KeyData)
		changed = true
	}
	errorChanged := false
	if errMsg := strings.TrimSpace(input.Error); errMsg != rec.Error {
		rec.Error = errMsg
		errorChanged = true
		changed = true
	}
	newState := DeriveState(rec.Status, rec.Error, input.State)
	stateChanged := rec.State != newState
	if rec.State != newState {
		rec.State = newState
		changed = true
	}
	// UpdatedAt is refreshed on every call — both meaningful state
	// changes and no-op ticks. GC anchors on UpdatedAt so any activity
	// (poller tick, status-call-driven update) extends the record's
	// visibility window. LastPayloadChangeAt (set inside the "changed"
	// branch below) remains the anchor for change detection so a stable
	// stream of identical responses does not spam change subscribers.
	rec.UpdatedAt = time.Now()
	if changed {
		m.updateChangedCount.Add(1)
		nextDigest := changeDigest(rec.Status, rec.Message, rec.State, rec.KeyData)
		meaningfulSignal := nextDigest != rec.changeDigest
		if !meaningfulSignal && percentChanged && shouldSignalPercentChange(rec.LastSignaledPercent, rec.Percent) {
			meaningfulSignal = true
			rec.LastSignaledPercent = cloneIntPtr(rec.Percent)
		}
		if meaningfulSignal {
			priorDigest := rec.changeDigest
			rec.changeDigest = nextDigest
			if rec.LastSignaledPercent == nil && rec.Percent != nil {
				rec.LastSignaledPercent = cloneIntPtr(rec.Percent)
			}
			rec.LastPayloadChangeAt = rec.UpdatedAt
			rec.pendingChange = true
			m.signalLocked(turnKey(rec.ParentConvID, rec.ParentTurnID))
			m.publishChangeLocked(rec, priorDigest, nextDigest)
		}
		if errorChanged && !stateChanged {
			rec.Error = strings.TrimSpace(input.Error)
		}
	}
	return rec, changed
}

func (m *Manager) ActiveOps(_ context.Context, convID, turnID string) []*OperationRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := turnKey(convID, turnID)
	var result []*OperationRecord
	for _, rec := range m.ops {
		if turnKey(rec.ParentConvID, rec.ParentTurnID) != key || rec.Terminal() {
			continue
		}
		result = append(result, cloneRecord(rec))
	}
	return result
}

func (m *Manager) OperationsForTurn(_ context.Context, convID, turnID string) []*OperationRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := turnKey(convID, turnID)
	var result []*OperationRecord
	for _, rec := range m.ops {
		if turnKey(rec.ParentConvID, rec.ParentTurnID) != key {
			continue
		}
		result = append(result, cloneRecord(rec))
	}
	return result
}

// ListOperations returns non-terminal operations matching the filter.
// Terminal ops (success / failure / canceled) are excluded by construction:
// they are pruned by GC and carry no actionable state for callers
// enumerating outstanding work. Callers that want a historical view should
// query the conversation message store, not the live manager.
//
// Tool-agnostic: an op from any source (internal service, MCP, system,
// user tool) is projected through the same `PendingOp` shape.
func (m *Manager) ListOperations(f Filter) []PendingOp {
	m.mu.Lock()
	defer m.mu.Unlock()
	convID := strings.TrimSpace(f.ConversationID)
	toolFilter := strings.TrimSpace(f.Tool)
	modeFilter := strings.TrimSpace(strings.ToLower(f.ExecutionMode))
	var out []PendingOp
	for _, rec := range m.ops {
		if rec == nil || rec.Terminal() {
			continue
		}
		if convID != "" && strings.TrimSpace(rec.ParentConvID) != convID {
			continue
		}
		if toolFilter != "" && !strings.EqualFold(strings.TrimSpace(rec.ToolName), toolFilter) {
			continue
		}
		if modeFilter != "" && strings.ToLower(strings.TrimSpace(rec.ExecutionMode)) != modeFilter {
			continue
		}
		op := PendingOp{
			OperationID:    rec.ID,
			Tool:           rec.ToolName,
			StatusTool:     rec.StatusToolName,
			OperationIDArg: rec.StatusOperationIDArg,
			SameToolRecall: rec.SameToolRecall,
			ExecutionMode:  rec.ExecutionMode,
			State:          rec.State,
			Intent:         rec.OperationIntent,
			Summary:        rec.OperationSummary,
			UpdatedAt:      rec.UpdatedAt,
			// Always surface the ready-to-send args map. The LLM can
			// then invoke StatusTool with StatusArgs verbatim, which
			// includes the op id under OperationIDArg plus any declared
			// ExtraArgs. OperationIDArg / SameToolRecall remain for
			// introspection. Deep-cloned so a consumer cannot mutate
			// Manager state through a shared nested reference.
			StatusArgs: deepCloneMap(rec.StatusArgs),
		}
		out = append(out, op)
	}
	return out
}

func (m *Manager) HasActiveWaitOps(ctx context.Context, convID, turnID string) bool {
	for _, rec := range m.ActiveOps(ctx, convID, turnID) {
		if ExecutionModeWaits(rec.ExecutionMode) {
			return true
		}
	}
	return false
}

func (m *Manager) ActiveWaitOps(ctx context.Context, convID, turnID string) []*OperationRecord {
	ops := m.ActiveOps(ctx, convID, turnID)
	if len(ops) == 0 {
		return nil
	}
	result := make([]*OperationRecord, 0, len(ops))
	for _, rec := range ops {
		if rec == nil || !ExecutionModeWaits(rec.ExecutionMode) {
			continue
		}
		result = append(result, rec)
	}
	return result
}

func (m *Manager) TerminalFailure(_ context.Context, convID, turnID string) (*OperationRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := turnKey(convID, turnID)
	for _, rec := range m.ops {
		if turnKey(rec.ParentConvID, rec.ParentTurnID) != key {
			continue
		}
		switch rec.State {
		case StateFailed, StateCanceled:
			return cloneRecord(rec), true
		}
	}
	return nil, false
}

func (m *Manager) WaitForChange(ctx context.Context, convID, turnID string) error {
	m.mu.Lock()
	key := turnKey(convID, turnID)
	for _, rec := range m.ops {
		if turnKey(rec.ParentConvID, rec.ParentTurnID) == key && rec.pendingChange {
			m.mu.Unlock()
			return nil
		}
	}
	ch := make(chan struct{}, 1)
	m.waiters[key] = append(m.waiters[key], ch)
	m.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		return nil
	}
}

func (m *Manager) WaitForNextPoll(ctx context.Context, convID, turnID string) error {
	waitOps := m.ActiveWaitOps(ctx, convID, turnID)
	if len(waitOps) == 0 {
		return nil
	}
	now := time.Now()
	var delay time.Duration
	for _, rec := range waitOps {
		if rec == nil {
			continue
		}
		intervalMs := rec.PollIntervalMs
		if intervalMs <= 0 {
			continue
		}
		nextAt := rec.UpdatedAt.Add(time.Duration(intervalMs) * time.Millisecond)
		if !nextAt.After(now) {
			return nil
		}
		nextDelay := time.Until(nextAt)
		if delay == 0 || nextDelay < delay {
			delay = nextDelay
		}
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (m *Manager) FindActiveByRequest(_ context.Context, convID, turnID, toolName, requestArgsDigest string) (*OperationRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	convID = strings.TrimSpace(convID)
	turnID = strings.TrimSpace(turnID)
	toolName = strings.TrimSpace(toolName)
	requestArgsDigest = strings.TrimSpace(requestArgsDigest)
	if convID == "" || turnID == "" || toolName == "" || requestArgsDigest == "" {
		return nil, false
	}
	key := turnKey(convID, turnID)
	for _, rec := range m.ops {
		if rec == nil || rec.Terminal() {
			continue
		}
		if !ExecutionModeWaits(rec.ExecutionMode) || turnKey(rec.ParentConvID, rec.ParentTurnID) != key {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(rec.ToolName), toolName) {
			continue
		}
		if rec.RequestArgsDigest != requestArgsDigest {
			continue
		}
		return cloneRecord(rec), true
	}
	return nil, false
}

func (m *Manager) ConsumeChanged(convID, turnID string) []*OperationRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := turnKey(convID, turnID)
	var result []*OperationRecord
	for _, rec := range m.ops {
		if turnKey(rec.ParentConvID, rec.ParentTurnID) != key || !rec.pendingChange {
			continue
		}
		rec.pendingChange = false
		result = append(result, cloneRecord(rec))
	}
	return result
}

// Subscribe returns a channel that receives ChangeEvents for any of the
// given op ids. The channel is buffered (16) and non-blocking on send —
// slow consumers drop events. Callers must re-read op state on each
// received event and must not treat the absence of an event as absence
// of change.
//
// The returned subscribe handle can be used with Unsubscribe to release
// the subscription early (important for callers that abandon the
// channel before all targets reach terminal — otherwise the
// subscription lingers and blocks GC of ops it references).
//
// When all targets are already terminal at subscribe time, the channel
// is closed immediately and subscribeID is 0.
func (m *Manager) Subscribe(opIDs []string) (<-chan ChangeEvent, uint64) {
	ch := make(chan ChangeEvent, 16)
	m.mu.Lock()
	defer m.mu.Unlock()
	targets := make(map[string]struct{}, len(opIDs))
	for _, opID := range opIDs {
		if id := strings.TrimSpace(opID); id != "" {
			targets[id] = struct{}{}
		}
	}
	if len(targets) == 0 || m.allTargetsTerminalLocked(targets) {
		close(ch)
		return ch, 0
	}
	m.nextSubID++
	subID := m.nextSubID
	m.subscriptions[subID] = &changeSubscription{
		targets: targets,
		ch:      ch,
	}
	m.subscribeCount.Add(1)
	return ch, subID
}

// Unsubscribe releases a subscription identified by the id returned
// from Subscribe. Safe to call multiple times and with id == 0 (no-op).
// After Unsubscribe, the subscription no longer pins its target ops
// against GC and no further events are delivered on the associated
// channel. The channel is closed if it had not already been closed by
// all-targets-terminal.
//
// Consumers that finish with a subscription (e.g. barrier waiter that
// returned an aggregated result, narrator that saw its parked call
// terminate) should call Unsubscribe to avoid lingering GC pins.
func (m *Manager) Unsubscribe(subID uint64) {
	if subID == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sub, ok := m.subscriptions[subID]
	if !ok {
		return
	}
	delete(m.subscriptions, subID)
	m.unsubscribeCount.Add(1)
	if sub != nil && sub.ch != nil {
		// Drain a drop-pending slot and close. Safe even if a publisher
		// just wrote — the buffered channel holds the event until
		// receive, and close signals no more will arrive.
		close(sub.ch)
	}
}

func (m *Manager) AwaitTerminal(ctx context.Context, opIDs []string) <-chan AggregatedResult {
	out := make(chan AggregatedResult, 1)
	go func() {
		defer close(out)
		result, done, wait := m.evaluateAwait(opIDs)
		if done {
			out <- result
			return
		}
		sub, subID := m.Subscribe(opIDs)
		// Always release the subscription on exit — abandoned ctx, channel
		// close, or terminal emission. Without this, the subscription
		// lingers in Manager.subscriptions indefinitely, pinning op ids
		// against GC (see §8 in doc/async.md).
		defer m.Unsubscribe(subID)
		timer := time.NewTimer(wait)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-sub:
				if !ok {
					result, done, _ = m.evaluateAwait(opIDs)
					if done {
						out <- result
					}
					return
				}
			case <-timer.C:
			}
			result, done, wait = m.evaluateAwait(opIDs)
			if done {
				out <- result
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(wait)
		}
	}()
	return out
}

// Sweep prunes op records that are safe to forget.
//
// An op is a prune candidate when all of the following hold:
//
//  1. It is terminal (success / failure / canceled) OR it is a detach-mode op
//     (no barrier and no auto-poller is attached to it — the record is
//     orphaned once detach is chosen).
//  2. Its last update is older than maxAge.
//  3. No current subscription references its id (pruning a subscribed op
//     would silently strand a waiter, so we never do it).
//
// Wait / fork ops that are still non-terminal are never pruned — they
// represent live work the barrier or a future status call is expected to
// observe. Callers that want timely cleanup of those should cancel them
// first so they transition to a terminal state.
//
// Sweep is safe to call concurrently with other Manager operations. It
// returns the number of ops removed. maxAge <= 0 is a no-op.
func (m *Manager) Sweep(now time.Time, maxAge time.Duration) int {
	if maxAge <= 0 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pruned := 0
	for id, rec := range m.ops {
		if rec == nil {
			m.dropOpLocked(id)
			pruned++
			continue
		}
		if m.opHasSubscriptionLocked(id) {
			continue
		}
		if now.Sub(rec.UpdatedAt) < maxAge {
			continue
		}
		// Non-terminal wait/fork ops are live work; do not prune them even
		// when they go idle. The barrier or a future status call owns them.
		if !rec.Terminal() && ExecutionModeWaits(rec.ExecutionMode) {
			continue
		}
		m.dropOpLocked(id)
		pruned++
	}
	if pruned > 0 {
		m.sweepPruneCount.Add(int64(pruned))
	}
	return pruned
}

// StartGC runs Sweep in a background goroutine until ctx is canceled
// OR the Manager is Close()d. Both signals cancel the goroutine
// immediately; whichever fires first wins.
//
// interval controls the sweep cadence; maxAge is forwarded to Sweep.
// Both arguments MUST be positive — this package holds no defaults of
// its own. When either is non-positive, StartGC returns without
// launching a goroutine. The authoritative defaults live in the
// workspace `default.async` baseline (see
// `workspace/config.DefaultsWithFallback`); bootstrap resolves them
// to durations and forwards the values here via
// `protocol/async/wsconfig.WorkspaceConfig.Apply`.
//
// The goroutine is cheap (one timer + one lock acquisition per tick)
// and exits cleanly. Callers that prefer explicit pacing (e.g. tied
// to a request/turn boundary) can call Sweep directly instead.
//
// StartGC is a no-op after Close — the Manager is no longer in a
// valid steady state, so spawning a fresh sweeper would race with the
// teardown that already happened.
func (m *Manager) StartGC(ctx context.Context, interval, maxAge time.Duration) {
	if ctx == nil || interval <= 0 || maxAge <= 0 {
		return
	}
	// Derive a child ctx the Manager can cancel during Close. The caller's
	// ctx remains authoritative; we just add a second cancellation trigger.
	gcCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		close(done)
		return
	}
	m.gcCancels = append(m.gcCancels, cancel)
	m.gcDone = append(m.gcDone, done)
	m.mu.Unlock()

	go func() {
		defer close(done)
		defer cancel() // idempotent; ensures the child ctx releases even if the parent never cancels
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-gcCtx.Done():
				return
			case now := <-ticker.C:
				m.Sweep(now, maxAge)
			}
		}
	}()
}

// Close releases Manager-owned resources synchronously. It:
//
//  1. Cancels every active poller (equivalent to CancelTurnPollers
//     across all turns) AND waits for each admitted poller goroutine
//     to exit via pollerWG.
//  2. Cancels every StartGC goroutine and waits for each to exit, so
//     there are no leaked sweepers after Close returns.
//  3. Closes every in-flight subscription channel (waking any consumers
//     that were blocked on receive so they can exit cleanly).
//  4. Signals every waiter from WaitForChange / WaitForNextPoll so
//     their ctx-less partners see a wakeup.
//
// After Close, no new Register / Subscribe / AwaitTerminal / Sweep /
// StartGC / AdmitPoller / TryStartPoller calls should be made — the
// Manager is no longer in a valid steady state. Post-close
// admissions/launches short-circuit. Close is idempotent: subsequent
// calls are no-ops.
//
// Close is synchronous for every goroutine the Manager tracks: when it
// returns, all GC sweepers AND all admitted poller goroutines have
// exited. This is the correctness guarantee the cross-package poller
// code (service/shared/toolexec.PollAsyncOperation) relies on:
// canceled ctx propagates to the poll loop; the defer-chain calls
// FinishPoller; Close blocks on pollerWG until every such defer has
// run.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	// Snapshot the GC cancels + done chans under the lock, then fire
	// cancels outside the lock and wait for the done signals. Waiting
	// under m.mu would deadlock because Sweep acquires m.mu.
	gcCancels := m.gcCancels
	gcDone := m.gcDone
	m.gcCancels = nil
	m.gcDone = nil
	// Cancel all registered poller cancel functions. Do NOT drain
	// m.pollers — each admitted poller's FinishPoller is the sole
	// deregistration site and decrements pollerWG on exit; clearing the
	// map here would defeat the wg-tracks-goroutine invariant.
	for id, cancel := range m.pollerCancels {
		if cancel != nil {
			cancel()
		}
		delete(m.pollerCancels, id)
	}
	// Close every subscription channel. Waking receivers is safe; they
	// see `!ok` on the channel and fall through their teardown paths.
	// Clear the subscription map.
	for subID, sub := range m.subscriptions {
		if sub != nil && sub.ch != nil {
			// Recover from close-of-closed panic if a publisher already
			// closed this channel (can happen when all targets went
			// terminal concurrently with Close).
			func(ch chan ChangeEvent) {
				defer func() { _ = recover() }()
				close(ch)
			}(sub.ch)
		}
		delete(m.subscriptions, subID)
	}
	// Signal every waiter so blocked WaitForChange / WaitForNextPoll
	// callers get a wakeup. The waiter map key is turnKey so we can't
	// call signalLocked per-turn without knowing them; drain all
	// waiters directly.
	for key, waiters := range m.waiters {
		for _, w := range waiters {
			select {
			case w <- struct{}{}:
			default:
			}
			close(w)
		}
		delete(m.waiters, key)
	}
	m.mu.Unlock()

	// Cancel GC goroutines outside the lock (Sweep acquires m.mu), then
	// wait for each to exit so Close's contract ("synchronous for
	// Manager-owned goroutines") holds.
	for _, cancel := range gcCancels {
		if cancel != nil {
			cancel()
		}
	}
	for _, done := range gcDone {
		if done != nil {
			<-done
		}
	}
	// Wait for every admitted poller goroutine to exit. Their cancel
	// funcs fired above; now their poll loops observe ctx.Done and run
	// their FinishPoller defers, each of which Done()s the wg. Waiting
	// must happen outside m.mu because FinishPoller briefly acquires it.
	m.pollerWG.Wait()
}

// opHasSubscriptionLocked reports whether any current subscription
// lists the given op id among its targets. Callers must hold m.mu.
func (m *Manager) opHasSubscriptionLocked(id string) bool {
	for _, sub := range m.subscriptions {
		if sub == nil {
			continue
		}
		if _, ok := sub.targets[id]; ok {
			return true
		}
	}
	return false
}

// dropOpLocked removes the op record and signals any attached poller
// to exit. Callers must hold m.mu.
//
// IMPORTANT: dropOpLocked does NOT delete m.pollers[id]. The admitted
// poller goroutine's own FinishPoller call is the sole deregistration
// site — it decrements pollerWG, which Close() waits on. Deleting the
// map entry here would decouple the wg from goroutine lifecycle: Close
// could return before the goroutine actually exited. The cancel func is
// fired so the goroutine stops quickly; the goroutine then deregisters
// itself on exit.
func (m *Manager) dropOpLocked(id string) {
	delete(m.ops, id)
	if cancel, ok := m.pollerCancels[id]; ok {
		cancel()
		delete(m.pollerCancels, id)
	}
	delete(m.waiters, id)
}

func cloneRecord(rec *OperationRecord) *OperationRecord {
	if rec == nil {
		return nil
	}
	copyRec := *rec
	copyRec.Percent = cloneIntPtr(rec.Percent)
	copyRec.LastSignaledPercent = cloneIntPtr(rec.LastSignaledPercent)
	copyRec.KeyData = cloneJSON(rec.KeyData)
	// Deep-clone arg maps. cloneMap was shallow — nested maps/slices
	// were shared with the canonical record, so a consumer that mutated
	// a returned StatusArgs["opts"].x would mutate Manager state.
	copyRec.RequestArgs = deepCloneMap(rec.RequestArgs)
	copyRec.StatusArgs = deepCloneMap(rec.StatusArgs)
	return &copyRec
}

func (m *Manager) allTargetsTerminalLocked(targets map[string]struct{}) bool {
	if len(targets) == 0 {
		return true
	}
	for opID := range targets {
		rec := m.ops[opID]
		// Treat missing records as terminal: if the op is gone (pruned by the
		// GC or never registered), no further transitions are possible, so the
		// subscriber cannot be waiting for them.
		if rec != nil && !rec.Terminal() {
			return false
		}
	}
	return true
}

func (m *Manager) publishChangeLocked(rec *OperationRecord, priorDigest, newDigest string) {
	if rec == nil || len(m.subscriptions) == 0 {
		return
	}
	ev := ChangeEvent{
		OperationID:  rec.ID,
		PriorDigest:  priorDigest,
		NewDigest:    newDigest,
		Status:       rec.Status,
		Message:      rec.Message,
		MessageKind:  rec.MessageKind,
		Percent:      cloneIntPtr(rec.Percent),
		KeyData:      cloneJSON(rec.KeyData),
		Error:        rec.Error,
		State:        rec.State,
		ChangedAt:    rec.UpdatedAt,
		ToolName:     rec.ToolName,
		Intent:       rec.OperationIntent,
		Summary:      rec.OperationSummary,
		Conversation: rec.ParentConvID,
		TurnID:       rec.ParentTurnID,
	}
	for id, sub := range m.subscriptions {
		if sub == nil {
			delete(m.subscriptions, id)
			continue
		}
		if _, ok := sub.targets[rec.ID]; !ok {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
		}
		if m.allTargetsTerminalLocked(sub.targets) {
			close(sub.ch)
			delete(m.subscriptions, id)
		}
	}
}

func (m *Manager) evaluateAwait(opIDs []string) (AggregatedResult, bool, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	result := AggregatedResult{}
	done := true
	nextWait := time.Duration(DefaultIdleTimeoutMs) * time.Millisecond
	if nextWait <= 0 {
		nextWait = time.Second
	}

	for _, opID := range opIDs {
		id := strings.TrimSpace(opID)
		if id == "" {
			continue
		}
		rec := m.ops[id]
		if rec == nil {
			continue
		}
		item := AggregatedItem{
			OperationID:      rec.ID,
			ToolName:         rec.ToolName,
			OperationIntent:  rec.OperationIntent,
			OperationSummary: rec.OperationSummary,
			State:            rec.State,
			Detail:           rec.Message,
			Payload:          cloneJSON(rec.KeyData),
		}
		if rec.Terminal() {
			item.Reason = aggregatedReason(rec, false)
		} else {
			idleMs := rec.IdleTimeoutMs
			if idleMs <= 0 {
				idleMs = DefaultIdleTimeoutMs
			}
			idleAt := rec.LastPayloadChangeAt.Add(time.Duration(idleMs) * time.Millisecond)
			if !idleAt.After(now) {
				item.Reason = "running_idle"
				result.OpsStillActive = true
			} else {
				done = false
				wait := time.Until(idleAt)
				if wait > 0 && wait < nextWait {
					nextWait = wait
				}
				item.Reason = strings.TrimSpace(string(rec.State))
				result.OpsStillActive = true
			}
		}
		result.Items = append(result.Items, item)
	}
	if done {
		return result, true, 0
	}
	if nextWait <= 0 {
		nextWait = time.Millisecond
	}
	for _, item := range result.Items {
		if item.Reason == "running_idle" {
			return result, true, 0
		}
	}
	return result, false, nextWait
}

func aggregatedReason(rec *OperationRecord, idle bool) string {
	if rec == nil {
		return ""
	}
	if idle {
		return "running_idle"
	}
	switch rec.State {
	case StateCompleted:
		return "success"
	case StateFailed:
		return "failure"
	case StateCanceled:
		return "canceled"
	case StateWaiting:
		return "waiting"
	case StateRunning:
		return "running"
	case StateStarted:
		return "started"
	default:
		return strings.TrimSpace(string(rec.State))
	}
}

func (m *Manager) RecordPollFailure(_ context.Context, id, errMsg string, transient bool) (*OperationRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.ops[strings.TrimSpace(id)]
	if rec == nil {
		return nil, false
	}
	rec.PollFailures++
	rec.Error = strings.TrimSpace(errMsg)
	rec.UpdatedAt = time.Now()
	if transient && rec.PollFailures < DefaultMaxPollFailures {
		return cloneRecord(rec), false
	}
	rec.State = StateFailed
	rec.Status = "failed"
	rec.pendingChange = true
	rec.changeDigest = changeDigest(rec.Status, rec.Message, rec.State, rec.KeyData)
	m.signalLocked(turnKey(rec.ParentConvID, rec.ParentTurnID))
	return cloneRecord(rec), true
}

func (m *Manager) ResetPollFailures(_ context.Context, id string) (*OperationRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.ops[strings.TrimSpace(id)]
	if rec == nil {
		return nil, false
	}
	if rec.PollFailures == 0 {
		return cloneRecord(rec), false
	}
	rec.PollFailures = 0
	rec.UpdatedAt = time.Now()
	return cloneRecord(rec), true
}

func DeriveState(status, errMsg string, requested State) State {
	if requested != "" {
		return requested
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "succeeded", "success", "done":
		return StateCompleted
	case "failed", "error", "rejected":
		return StateFailed
	case "canceled", "cancelled", "cancel":
		return StateCanceled
	case "queued", "pending", "waiting", "waiting_for_user":
		return StateWaiting
	case "running", "open", "processing":
		return StateRunning
	}
	if strings.TrimSpace(errMsg) != "" {
		return StateFailed
	}
	return StateStarted
}

func (m *Manager) signalLocked(key string) {
	waiters := m.waiters[key]
	delete(m.waiters, key)
	for _, waiter := range waiters {
		select {
		case waiter <- struct{}{}:
		default:
		}
		close(waiter)
	}
}

func turnKey(convID, turnID string) string {
	return strings.TrimSpace(convID) + "|" + strings.TrimSpace(turnID)
}

func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}

func cloneJSON(v json.RawMessage) json.RawMessage {
	if len(v) == 0 {
		return nil
	}
	dup := make([]byte, len(v))
	copy(dup, v)
	return dup
}

func equalIntPtr(a, b *int) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

func jsonEqual(a, b json.RawMessage) bool {
	return string(a) == string(b)
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func changeDigest(status, message string, state State, keyData json.RawMessage) string {
	type digestView struct {
		Status  string          `json:"status,omitempty"`
		Message string          `json:"message,omitempty"`
		State   string          `json:"state,omitempty"`
		KeyData json.RawMessage `json:"keyData,omitempty"`
	}
	payload, _ := json.Marshal(digestView{
		Status:  strings.TrimSpace(status),
		Message: strings.TrimSpace(message),
		State:   strings.TrimSpace(string(state)),
		KeyData: cloneJSON(keyData),
	})
	sum := md5.Sum(payload)
	return hex.EncodeToString(sum[:])
}

func shouldSignalPercentChange(previous, current *int) bool {
	if current == nil {
		return false
	}
	if previous == nil {
		return true
	}
	diff := *current - *previous
	if diff < 0 {
		diff = -diff
	}
	return diff >= DefaultPercentSignalThreshold
}
