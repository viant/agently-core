package oauthlinkstate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	current "github.com/viant/agently-core/internal/datly/oauth/linkstate/read"
	write "github.com/viant/agently-core/internal/datly/oauth/linkstate/write"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

const timeLayout = "2006-01-02 15:04:05"

// ErrInvalidState deliberately gives every rejected consume the same public result.
var ErrInvalidState = errors.New("oauth link state is invalid")

// Record contains only non-secret state metadata. The authorization code,
// verifier, token, and encrypted browser state never enter this store.
type Record struct {
	StateHash       string
	FlowHash        string
	CanonicalUserID string
	SessionHash     string
	Provider        string
	ExpiresAt       time.Time
	ConsumedAt      *time.Time
	CreatedAt       time.Time
}

func (r *Record) Pending(now time.Time) bool {
	return r != nil && r.ConsumedAt == nil && r.ExpiresAt.After(now)
}

// Store maps the existing OAuth state lifecycle to the generated Datly 1.0
// reader and writer. All persistence, predicates, and CAS remain in those
// contracts; this adapter holds no database handle.
type Store struct {
	Invoker dexec.ComponentInvoker
	Now     func() time.Time
}

var readerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[current.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/user/oauth/linkstate/current"},
}

var writerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriteComponent]().PkgPath(), Name: "write"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/user/oauth/linkstate/write"},
}

func (s *Store) clock() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Store) reader(ctx context.Context, input *current.LinkStateInput) ([]*current.LinkStateView, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("oauth state store is not configured")
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: input})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*current.LinkStateOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("oauth state reader returned %T", value)
	}
	return out.Data, nil
}

func (s *Store) writer(ctx context.Context, input *write.Input) (*write.Output, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("oauth state store is not configured")
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: writerTarget, Input: input})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*write.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("oauth state writer returned %T", value)
	}
	return out, nil
}

// CreateOrGetPending preserves flow deduplication and winner selection in the
// generated writer's transaction and recovery hooks.
func (s *Store) CreateOrGetPending(ctx context.Context, record *Record) (*Record, bool, error) {
	if record == nil {
		return nil, false, fmt.Errorf("oauth state record is required")
	}
	state := &write.LinkState{}
	state.SetStateHash(strings.TrimSpace(record.StateHash))
	state.SetFlowHash(strings.TrimSpace(record.FlowHash))
	state.SetUserId(strings.TrimSpace(record.CanonicalUserID))
	state.SetSessionHash(strings.TrimSpace(record.SessionHash))
	state.SetProvider(strings.TrimSpace(record.Provider))
	state.SetExpiresAt(record.ExpiresAt.UTC().Format(timeLayout))
	state.Now = s.clock().Format(timeLayout)
	input := &write.Input{}
	input.SetMode("create")
	input.SetStates([]*write.LinkState{state})
	out, err := s.writer(ctx, input)
	if err != nil {
		return nil, false, err
	}
	if len(out.Data) != 1 {
		return nil, false, fmt.Errorf("oauth state writer returned %d rows", len(out.Data))
	}
	stored, err := fromWrite(out.Data[0])
	return stored, out.Created, err
}

// GetPending is used for flow status polling. The pending check is repeated
// after reading so expiry cannot be mistaken for a live authorization URL.
func (s *Store) GetPending(ctx context.Context, flowHash string) (*Record, error) {
	input := &current.LinkStateInput{}
	input.SetFlowHash(strings.TrimSpace(flowHash))
	input.SetPending(true)
	rows, err := s.reader(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(rows) > 1 {
		return nil, fmt.Errorf("oauth flow returned multiple rows")
	}
	if len(rows) == 0 {
		return nil, nil
	}
	record, err := fromRead(rows[0])
	if err != nil || !record.Pending(s.clock()) {
		return nil, err
	}
	return record, nil
}

// Consume first resolves the flow key from the state hash, then lets the
// generated writer classify and atomically transition the exact row. Every
// rejection, including a missing state, is intentionally non-enumerable.
func (s *Store) Consume(ctx context.Context, stateHash, canonicalUserID, sessionHash string) error {
	stateHash = strings.TrimSpace(stateHash)
	input := &current.LinkStateInput{}
	input.SetStateHash(stateHash)
	rows, err := s.reader(ctx, input)
	if err != nil {
		return err
	}
	if len(rows) > 1 {
		return fmt.Errorf("oauth state hash returned multiple rows")
	}
	if len(rows) == 0 {
		return ErrInvalidState
	}
	state := &write.LinkState{}
	state.SetFlowHash(rows[0].FlowHash)
	state.SetStateHash(stateHash)
	state.SetUserId(strings.TrimSpace(canonicalUserID))
	state.SetSessionHash(strings.TrimSpace(sessionHash))
	state.Now = s.clock().Format(timeLayout)
	mutation := &write.Input{}
	mutation.SetMode("consume")
	mutation.SetStates([]*write.LinkState{state})
	out, err := s.writer(ctx, mutation)
	if err != nil {
		return err
	}
	if out.Outcome != "consumed" {
		return ErrInvalidState
	}
	return nil
}

// DeleteExpired selects candidates with the canonical reader and lets the
// writer recheck expiry for each row inside its transaction. A stale batch
// fails atomically and can be retried by the next scheduled cleanup.
func (s *Store) DeleteExpired(ctx context.Context, before time.Time) (int64, time.Time, error) {
	horizon := before.UTC().Format(timeLayout)
	query := &current.LinkStateInput{}
	query.SetMode("expired")
	query.SetBefore(horizon)
	rows, err := s.reader(ctx, query)
	if err != nil {
		return 0, time.Time{}, err
	}
	states := make([]*write.LinkState, 0, len(rows))
	for _, row := range rows {
		state := &write.LinkState{}
		state.SetFlowHash(row.FlowHash)
		state.SetShouldDelete(true)
		states = append(states, state)
	}
	mutation := &write.Input{}
	mutation.SetMode("cleanup")
	mutation.SetBefore(horizon)
	mutation.SetStates(states)
	out, err := s.writer(ctx, mutation)
	if err != nil {
		return 0, time.Time{}, err
	}
	if out.OldestExpiresAt == "" {
		return int64(out.Deleted), time.Time{}, nil
	}
	oldest, err := parseTime(out.OldestExpiresAt)
	return int64(out.Deleted), oldest, err
}

func fromWrite(row *write.LinkState) (*Record, error) {
	if row == nil {
		return nil, fmt.Errorf("oauth state writer returned nil row")
	}
	expires, err := parseTime(row.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("oauth state expiry: %w", err)
	}
	record := &Record{StateHash: row.StateHash, FlowHash: row.FlowHash, CanonicalUserID: row.UserId, SessionHash: row.SessionHash, Provider: row.Provider, ExpiresAt: expires}
	if row.ConsumedAt != nil && strings.TrimSpace(*row.ConsumedAt) != "" {
		consumed, err := parseTime(*row.ConsumedAt)
		if err != nil {
			return nil, fmt.Errorf("oauth state consumption: %w", err)
		}
		record.ConsumedAt = &consumed
	}
	if strings.TrimSpace(row.CreatedAt) != "" {
		record.CreatedAt, err = parseTime(row.CreatedAt)
	}
	return record, err
}

func fromRead(row *current.LinkStateView) (*Record, error) {
	if row == nil {
		return nil, fmt.Errorf("oauth state reader returned nil row")
	}
	expires, err := parseTime(row.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("oauth state expiry: %w", err)
	}
	record := &Record{StateHash: row.StateHash, FlowHash: row.FlowHash, CanonicalUserID: row.UserId, SessionHash: row.SessionHash, Provider: row.Provider, ExpiresAt: expires}
	if row.ConsumedAt != nil && strings.TrimSpace(*row.ConsumedAt) != "" {
		consumed, err := parseTime(*row.ConsumedAt)
		if err != nil {
			return nil, fmt.Errorf("oauth state consumption: %w", err)
		}
		record.ConsumedAt = &consumed
	}
	if strings.TrimSpace(row.CreatedAt) != "" {
		record.CreatedAt, err = parseTime(row.CreatedAt)
	}
	return record, err
}

func parseTime(value string) (time.Time, error) {
	for _, layout := range []string{timeLayout, time.RFC3339Nano, "2006-01-02T15:04:05"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid oauth state timestamp")
}
