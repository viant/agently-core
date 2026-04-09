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
	DefaultMaxReinforcementsPerOperation = 10
	DefaultMinIntervalBetweenMs          = 30000
	DefaultPercentSignalThreshold        = 5
	DefaultMaxPollFailures               = 3
)

type OperationRecord struct {
	ID                            string
	ParentConvID                  string
	ParentTurnID                  string
	ToolCallID                    string
	ToolMessageID                 string
	ToolName                      string
	RequestArgsDigest             string
	RequestArgs                   map[string]interface{}
	WaitForResponse               bool
	State                         State
	Status                        string
	Message                       string
	Percent                       *int
	LastSignaledPercent           *int
	KeyData                       json.RawMessage
	Error                         string
	CreatedAt                     time.Time
	UpdatedAt                     time.Time
	TimeoutAt                     *time.Time
	TimeoutMs                     int
	PollIntervalMs                int
	PollFailures                  int
	ReinforcementPrompt           string
	Reinforcement                 *PromptConfig
	MaxReinforcementsPerOperation int
	MinIntervalBetweenMs          int
	ReinforcementCount            int
	LastReinforcementAt           *time.Time
	changeDigest                  string
	pendingChange                 bool
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
	ID                            string
	ParentConvID                  string
	ParentTurnID                  string
	ToolCallID                    string
	ToolMessageID                 string
	ToolName                      string
	RequestArgsDigest             string
	RequestArgs                   map[string]interface{}
	WaitForResponse               bool
	Status                        string
	Message                       string
	Percent                       *int
	KeyData                       json.RawMessage
	Error                         string
	TimeoutMs                     int
	PollIntervalMs                int
	Reinforcement                 *PromptConfig
	ReinforcementPrompt           string
	MaxReinforcementsPerOperation int
	MinIntervalBetweenMs          int
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
	mu      sync.Mutex
	ops     map[string]*OperationRecord
	waiters map[string][]chan struct{}
}

func NewManager() *Manager {
	return &Manager{
		ops:     map[string]*OperationRecord{},
		waiters: map[string][]chan struct{}{},
	}
}

func (m *Manager) Register(_ context.Context, input RegisterInput) *OperationRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	rec := &OperationRecord{
		ID:                            strings.TrimSpace(input.ID),
		ParentConvID:                  strings.TrimSpace(input.ParentConvID),
		ParentTurnID:                  strings.TrimSpace(input.ParentTurnID),
		ToolCallID:                    strings.TrimSpace(input.ToolCallID),
		ToolMessageID:                 strings.TrimSpace(input.ToolMessageID),
		ToolName:                      strings.TrimSpace(input.ToolName),
		RequestArgsDigest:             strings.TrimSpace(input.RequestArgsDigest),
		RequestArgs:                   cloneMap(input.RequestArgs),
		WaitForResponse:               input.WaitForResponse,
		Status:                        strings.TrimSpace(input.Status),
		Message:                       strings.TrimSpace(input.Message),
		Percent:                       cloneIntPtr(input.Percent),
		LastSignaledPercent:           cloneIntPtr(input.Percent),
		KeyData:                       cloneJSON(input.KeyData),
		Error:                         strings.TrimSpace(input.Error),
		CreatedAt:                     now,
		UpdatedAt:                     now,
		TimeoutMs:                     input.TimeoutMs,
		PollIntervalMs:                input.PollIntervalMs,
		MaxReinforcementsPerOperation: input.MaxReinforcementsPerOperation,
		MinIntervalBetweenMs:          input.MinIntervalBetweenMs,
		ReinforcementPrompt:           strings.TrimSpace(input.ReinforcementPrompt),
		Reinforcement:                 clonePrompt(input.Reinforcement),
		pendingChange:                 true,
	}
	if input.TimeoutMs > 0 {
		timeoutAt := now.Add(time.Duration(input.TimeoutMs) * time.Millisecond)
		rec.TimeoutAt = &timeoutAt
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
			rec.changeDigest = nextDigest
			if rec.LastSignaledPercent == nil && rec.Percent != nil {
				rec.LastSignaledPercent = cloneIntPtr(rec.Percent)
			}
			rec.pendingChange = true
			m.signalLocked(turnKey(rec.ParentConvID, rec.ParentTurnID))
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
		if rec.WaitForResponse {
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
		if rec == nil || !rec.WaitForResponse {
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
		if !rec.WaitForResponse || turnKey(rec.ParentConvID, rec.ParentTurnID) != key {
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

func cloneRecord(rec *OperationRecord) *OperationRecord {
	if rec == nil {
		return nil
	}
	copyRec := *rec
	copyRec.Percent = cloneIntPtr(rec.Percent)
	copyRec.LastSignaledPercent = cloneIntPtr(rec.LastSignaledPercent)
	copyRec.KeyData = cloneJSON(rec.KeyData)
	copyRec.RequestArgs = cloneMap(rec.RequestArgs)
	if rec.LastReinforcementAt != nil {
		t := *rec.LastReinforcementAt
		copyRec.LastReinforcementAt = &t
	}
	copyRec.Reinforcement = clonePrompt(rec.Reinforcement)
	return &copyRec
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

func (m *Manager) TryRecordReinforcement(_ context.Context, id string) (*OperationRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.ops[strings.TrimSpace(id)]
	if rec == nil {
		return nil, false
	}
	now := time.Now()
	if !rec.Terminal() {
		max := rec.MaxReinforcementsPerOperation
		if max <= 0 {
			max = DefaultMaxReinforcementsPerOperation
		}
		if rec.ReinforcementCount >= max {
			return cloneRecord(rec), false
		}
		minInterval := rec.MinIntervalBetweenMs
		if minInterval <= 0 {
			minInterval = DefaultMinIntervalBetweenMs
		}
		if rec.LastReinforcementAt != nil && now.Sub(*rec.LastReinforcementAt) < time.Duration(minInterval)*time.Millisecond {
			return cloneRecord(rec), false
		}
	}
	rec.ReinforcementCount++
	rec.UpdatedAt = now
	rec.LastReinforcementAt = &now
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

func clonePrompt(p *PromptConfig) *PromptConfig {
	if p == nil {
		return nil
	}
	copyPrompt := *p
	return &copyPrompt
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
