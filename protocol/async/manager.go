package async

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
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
	ID                  string
	ParentConvID        string
	ParentTurnID        string
	ToolCallID          string
	ToolMessageID       string
	ToolName            string
	StatusToolName      string
	StatusArgs          map[string]interface{}
	CancelToolName      string
	RequestArgsDigest   string
	RequestArgs         map[string]interface{}
	OperationIntent     string
	OperationSummary    string
	ExecutionMode       string
	State               State
	Status              string
	Message             string
	Percent             *int
	LastSignaledPercent *int
	KeyData             json.RawMessage
	Error               string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LastPayloadChangeAt time.Time
	TimeoutAt           *time.Time
	TimeoutMs           int
	IdleTimeoutMs       int
	PollIntervalMs      int
	PollFailures        int
	changeDigest        string
	pendingChange       bool
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
	ID                string
	ParentConvID      string
	ParentTurnID      string
	ToolCallID        string
	ToolMessageID     string
	ToolName          string
	StatusToolName    string
	StatusArgs        map[string]interface{}
	CancelToolName    string
	RequestArgsDigest string
	RequestArgs       map[string]interface{}
	OperationIntent   string
	OperationSummary  string
	ExecutionMode     string
	Status            string
	Message           string
	Percent           *int
	KeyData           json.RawMessage
	Error             string
	TimeoutMs         int
	IdleTimeoutMs     int
	PollIntervalMs    int
}

type ChangeEvent struct {
	OperationID  string          `json:"operationId,omitempty"`
	PriorDigest  string          `json:"priorDigest,omitempty"`
	NewDigest    string          `json:"newDigest,omitempty"`
	Status       string          `json:"status,omitempty"`
	Message      string          `json:"message,omitempty"`
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

type changeSubscription struct {
	targets map[string]struct{}
	ch      chan ChangeEvent
}

type UpdateInput struct {
	ID      string
	Status  string
	Message string
	Percent *int
	KeyData json.RawMessage
	Error   string
	State   State
}

type Manager struct {
	mu            sync.Mutex
	ops           map[string]*OperationRecord
	pollers       map[string]struct{}
	pollerCancels map[string]context.CancelFunc
	waiters       map[string][]chan struct{}
	subscriptions map[uint64]*changeSubscription
	nextSubID     uint64
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

func (m *Manager) TryStartPoller(_ context.Context, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if m.pollers == nil {
		m.pollers = map[string]struct{}{}
	}
	if _, exists := m.pollers[id]; exists {
		return false
	}
	m.pollers[id] = struct{}{}
	return true
}

func (m *Manager) FinishPoller(_ context.Context, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pollers == nil {
		return
	}
	id = strings.TrimSpace(id)
	delete(m.pollers, id)
	// Clean up the stored cancel function; calling it here is intentionally
	// safe (context.CancelFunc is idempotent).
	if m.pollerCancels != nil {
		if cancel, ok := m.pollerCancels[id]; ok {
			cancel()
			delete(m.pollerCancels, id)
		}
	}
}

// StorePollerCancel registers a cancel function for the given operation so that
// CancelTurnPollers can reach it. Called immediately after TryStartPoller succeeds.
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

func (m *Manager) Register(_ context.Context, input RegisterInput) *OperationRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	rec := &OperationRecord{
		ID:                  strings.TrimSpace(input.ID),
		ParentConvID:        strings.TrimSpace(input.ParentConvID),
		ParentTurnID:        strings.TrimSpace(input.ParentTurnID),
		ToolCallID:          strings.TrimSpace(input.ToolCallID),
		ToolMessageID:       strings.TrimSpace(input.ToolMessageID),
		ToolName:            strings.TrimSpace(input.ToolName),
		StatusToolName:      strings.TrimSpace(input.StatusToolName),
		StatusArgs:          cloneMap(input.StatusArgs),
		CancelToolName:      strings.TrimSpace(input.CancelToolName),
		RequestArgsDigest:   strings.TrimSpace(input.RequestArgsDigest),
		RequestArgs:         cloneMap(input.RequestArgs),
		OperationIntent:     strings.TrimSpace(input.OperationIntent),
		OperationSummary:    strings.TrimSpace(input.OperationSummary),
		ExecutionMode:       NormalizeExecutionMode(input.ExecutionMode, string(ExecutionModeWait)),
		Status:              strings.TrimSpace(input.Status),
		Message:             strings.TrimSpace(input.Message),
		Percent:             cloneIntPtr(input.Percent),
		LastSignaledPercent: cloneIntPtr(input.Percent),
		KeyData:             cloneJSON(input.KeyData),
		Error:               strings.TrimSpace(input.Error),
		CreatedAt:           now,
		UpdatedAt:           now,
		LastPayloadChangeAt: now,
		TimeoutMs:           input.TimeoutMs,
		IdleTimeoutMs:       input.IdleTimeoutMs,
		PollIntervalMs:      input.PollIntervalMs,
		pendingChange:       true,
	}
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
	return rec
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
	changed := false
	if status := strings.TrimSpace(input.Status); status != "" && status != rec.Status {
		rec.Status = status
		changed = true
	}
	if msg := strings.TrimSpace(input.Message); msg != rec.Message {
		rec.Message = msg
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
	if changed {
		rec.UpdatedAt = time.Now()
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

func (m *Manager) Subscribe(opIDs []string) <-chan ChangeEvent {
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
		return ch
	}
	m.nextSubID++
	m.subscriptions[m.nextSubID] = &changeSubscription{
		targets: targets,
		ch:      ch,
	}
	return ch
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
		sub := m.Subscribe(opIDs)
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

func cloneRecord(rec *OperationRecord) *OperationRecord {
	if rec == nil {
		return nil
	}
	copyRec := *rec
	copyRec.Percent = cloneIntPtr(rec.Percent)
	copyRec.LastSignaledPercent = cloneIntPtr(rec.LastSignaledPercent)
	copyRec.KeyData = cloneJSON(rec.KeyData)
	copyRec.RequestArgs = cloneMap(rec.RequestArgs)
	copyRec.StatusArgs = cloneMap(rec.StatusArgs)
	return &copyRec
}

func (m *Manager) allTargetsTerminalLocked(targets map[string]struct{}) bool {
	if len(targets) == 0 {
		return true
	}
	for opID := range targets {
		rec := m.ops[opID]
		if rec == nil || !rec.Terminal() {
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
