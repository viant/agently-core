package write

import (
	"context"
	"errors"
	"fmt"
	current "github.com/viant/agently-core/internal/datly/oauth/linkstate/read"
	dexec "github.com/viant/datly/exec"
	rhandler "github.com/viant/datly/runtime/handler"
	"github.com/viant/datly/spec"
	"github.com/viant/sqlx/io/errx"
	xhandler "github.com/viant/xdatly/handler"
	"reflect"
	"strings"
	"time"
)

type Lifecycle struct {
	Input         *Input                 `bind:"kind=input"`
	Invoker       dexec.ComponentInvoker `bind:"kind=component_invoker"`
	cleanupCount  int
	cleanupOldest time.Time
}

func LifecycleDatlyType() reflect.Type { return reflect.TypeFor[Lifecycle]() }

var LifecycleHooks = new(Lifecycle)
var LifecycleDatly = LifecycleDatlyType()

const stateTimeLayout = "2006-01-02 15:04:05"

func stateTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{stateTimeLayout, time.RFC3339Nano, "2006-01-02T15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid state timestamp")
}

func (h *Lifecycle) Init(ctx context.Context, entity *LinkState, state xhandler.LifecycleContext[LinkState, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if h.Input == nil {
		return fmt.Errorf("canonical input is unavailable")
	}
	switch h.Input.Mode {
	case "", "create":
		if entity.ShouldDelete {
			return fmt.Errorf("create cannot delete a link state")
		}
	case "consume":
		return h.initConsume(entity, state)
	case "replace":
		return h.initReplace(entity, state)
	case "cleanup":
		return h.initCleanup(entity, state)
	default:
		return fmt.Errorf("unsupported link state mutation mode")
	}
	for name, value := range map[string]string{"stateHash": entity.StateHash, "flowHash": entity.FlowHash, "userId": entity.UserId, "sessionHash": entity.SessionHash, "provider": entity.Provider} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if _, err := time.Parse(stateTimeLayout, strings.TrimSpace(entity.ExpiresAt)); err != nil {
		return fmt.Errorf("expiresAt must use layout %q", stateTimeLayout)
	}
	now := strings.TrimSpace(entity.Now)
	if now == "" {
		now = time.Now().UTC().Format(stateTimeLayout)
	}
	anchor, err := stateTime(now)
	if err != nil {
		return err
	}
	if state.Previous != nil && state.Previous.ConsumedAt == nil {
		expires, err := stateTime(state.Previous.ExpiresAt)
		if err == nil && expires.After(anchor) {
			*entity = *state.Previous
			entity.Has = &LinkStateHas{FlowHash: true}
			entity.Now = now
			state.Output.Created = false
			return nil
		}
	}
	if h.Input == nil {
		return fmt.Errorf("canonical input is unavailable")
	}
	h.Input.SetNow(now)
	if state.Previous != nil {
		h.Input.SetObservedState(state.Previous.StateHash)
	}
	entity.SetConsumedAt(nil)
	entity.SetCreatedAt(now)
	state.Output.Created = true
	return nil
}
func (*Lifecycle) Validate(context.Context, *LinkState, xhandler.LifecycleContext[LinkState, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) AfterSequence(context.Context, *LinkState, xhandler.LifecycleContext[LinkState, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) AfterQueue(context.Context, *LinkState, xhandler.LifecycleContext[LinkState, xhandler.NoParent, Output]) error {
	return nil
}
func (h *Lifecycle) Finalize(_ context.Context, input *Input, out *Output, outcome xhandler.Outcome) error {
	if outcome.Error != nil {
		out.Created = false
		if input != nil && input.Mode == "consume" && out.Outcome == "consumed" {
			out.Outcome = ""
			out.Data = nil
		}
		out.Deleted = 0
		out.OldestExpiresAt = ""
	} else if out.Outcome != "" && out.Outcome != "consumed" {
		// Rejected single-use attempts expose only the audit classification.
		out.Data = nil
	}
	if input != nil && input.Mode == "cleanup" && outcome.CommitConfirmed() {
		out.Deleted = h.cleanupCount
		if h.cleanupCount > 0 {
			out.OldestExpiresAt = h.cleanupOldest.UTC().Format(stateTimeLayout)
		}
	}
	return nil
}

// Recover chooses native request replay for creation/CAS ownership losses.
// The engine owns the bounded attempt, fresh Current binding and transaction.
func (h *Lifecycle) Recover(ctx context.Context, input *Input, out *Output, outcome rhandler.MutationOutcome) (rhandler.Recovery, error) {
	if outcome.Mutation.Table != "oauth_link_state" {
		return rhandler.RecoveryNone, nil
	}
	if input != nil && input.Mode == "consume" {
		if outcome.Error == nil && out.Outcome != "" && out.Outcome != "consumed" {
			return rhandler.RecoveryAccept, nil
		}
		return h.recoverConsume(ctx, input, out, outcome)
	}
	if input != nil && (input.Mode == "replace" || input.Mode == "cleanup") {
		return rhandler.RecoveryNone, nil
	}
	if outcome.Error == nil && !out.Created {
		return rhandler.RecoveryAccept, nil
	}
	var conflict *xhandler.Conflict
	lost := outcome.Mutation.Error == nil && outcome.Mutation.Affected == 0
	lost = lost || errors.As(outcome.Mutation.Error, &conflict) || errx.IsDuplicateKey(outcome.Mutation.Error) || outcome.Mutation.Contention
	if !lost {
		return rhandler.RecoveryNone, nil
	}
	requested := singleState(input)
	if h.Invoker == nil || requested == nil {
		return rhandler.RecoveryNone, fmt.Errorf("typed winner reader is unavailable")
	}
	query := &current.LinkStateInput{}
	query.SetFlowHash(requested.FlowHash)
	value, err := h.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{
		Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[current.ReaderComponent]().PkgPath(), Name: "reader"},
		Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/user/oauth/linkstate/current"},
	}, Input: query})
	if err != nil {
		return rhandler.RecoveryNone, err
	}
	rows := value.(*current.LinkStateOutput).Data
	if len(rows) > 1 {
		return rhandler.RecoveryNone, fmt.Errorf("flow identity returned multiple winners")
	}
	if len(rows) == 0 && outcome.Mutation.Operation == "insert" {
		if outcome.Mutation.Error != nil {
			return rhandler.RecoveryNone, nil
		}
		return rhandler.RecoveryNone, fmt.Errorf("oauth link state insert affected no row")
	}
	var winner *LinkState
	pending := false
	if len(rows) == 1 {
		row := rows[0]
		winner = &LinkState{StateHash: row.StateHash, FlowHash: row.FlowHash, UserId: row.UserId, SessionHash: row.SessionHash, Provider: row.Provider, ExpiresAt: row.ExpiresAt, ConsumedAt: row.ConsumedAt, CreatedAt: row.CreatedAt}
		if winner.ConsumedAt == nil {
			expires, eErr := stateTime(winner.ExpiresAt)
			anchor, nErr := stateTime(requested.Now)
			if nErr != nil {
				anchor, nErr = stateTime(input.Now)
			}
			pending = eErr == nil && nErr == nil && expires.After(anchor)
		}
	}
	if !pending && outcome.Attempt < outcome.RetryLimit {
		return rhandler.RecoveryRetry, nil
	}
	out.Data, out.Created = nil, false
	if winner != nil {
		out.Data = []*LinkState{winner}
	}
	return rhandler.RecoveryAccept, nil
}

func (h *Lifecycle) initConsume(entity *LinkState, state xhandler.LifecycleContext[LinkState, xhandler.NoParent, Output]) error {
	if entity.ShouldDelete {
		return fmt.Errorf("consume cannot delete a link state")
	}
	requestedState := strings.TrimSpace(entity.StateHash)
	requestedUser := strings.TrimSpace(entity.UserId)
	requestedSession := strings.TrimSpace(entity.SessionHash)
	flow := strings.TrimSpace(entity.FlowHash)
	if requestedState == "" || requestedUser == "" || requestedSession == "" || flow == "" {
		return fmt.Errorf("stateHash, flowHash, userId and sessionHash are required")
	}
	now := strings.TrimSpace(entity.Now)
	if now == "" {
		now = time.Now().UTC().Format(stateTimeLayout)
	}
	anchor, err := stateTime(now)
	if err != nil {
		return err
	}
	if state.Previous == nil {
		entity.SetShouldDelete(true)
		state.Output.Outcome = "absent"
		return nil
	}
	previous := state.Previous
	reason := consumeClassification(previous.ConsumedAt, previous.ExpiresAt, previous.StateHash, previous.UserId, previous.SessionHash, requestedState, requestedUser, requestedSession, anchor)
	*entity = *previous
	entity.Has = &LinkStateHas{FlowHash: true}
	entity.Now = now
	state.Output.Outcome = reason
	if reason != "consumed" {
		return nil
	}
	entity.SetConsumedAt(&now)
	h.Input.SetExpectedState(requestedState)
	h.Input.SetExpectedUser(requestedUser)
	h.Input.SetExpectedSession(requestedSession)
	h.Input.SetUnconsumed(true)
	h.Input.SetNotExpiredAfter(now)
	return nil
}

func consumeClassification(consumedAt *string, expiresAt, stateHash, userID, sessionHash, requestedState, requestedUser, requestedSession string, now time.Time) string {
	if consumedAt != nil && strings.TrimSpace(*consumedAt) != "" {
		return "already_consumed"
	}
	expires, err := stateTime(expiresAt)
	if err != nil || !expires.After(now) {
		return "expired"
	}
	if strings.TrimSpace(stateHash) != requestedState {
		return "absent"
	}
	if strings.TrimSpace(userID) != requestedUser {
		return "user_mismatch"
	}
	if strings.TrimSpace(sessionHash) != requestedSession {
		return "session_mismatch"
	}
	return "consumed"
}

func (h *Lifecycle) recoverConsume(ctx context.Context, input *Input, out *Output, outcome rhandler.MutationOutcome) (rhandler.Recovery, error) {
	var conflict *xhandler.Conflict
	lost := outcome.Mutation.Error == nil && outcome.Mutation.Affected == 0
	lost = lost || errors.As(outcome.Mutation.Error, &conflict) || errx.IsDuplicateKey(outcome.Mutation.Error) || outcome.Mutation.Contention
	if !lost {
		return rhandler.RecoveryNone, nil
	}
	if h.Invoker == nil || input == nil || input.ExpectedState == "" {
		return rhandler.RecoveryNone, fmt.Errorf("typed state classifier is unavailable")
	}
	query := &current.LinkStateInput{}
	query.SetStateHash(input.ExpectedState)
	value, err := h.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{
		Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[current.ReaderComponent]().PkgPath(), Name: "reader"},
		Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/user/oauth/linkstate/current"},
	}, Input: query})
	if err != nil {
		return rhandler.RecoveryNone, err
	}
	rows := value.(*current.LinkStateOutput).Data
	if len(rows) > 1 {
		return rhandler.RecoveryNone, fmt.Errorf("state identity returned multiple rows")
	}
	out.Data = nil
	out.Created = false
	out.Outcome = "absent"
	if len(rows) == 1 {
		anchor, err := stateTime(input.NotExpiredAfter)
		if err != nil {
			return rhandler.RecoveryNone, err
		}
		row := rows[0]
		out.Outcome = consumeClassification(row.ConsumedAt, row.ExpiresAt, row.StateHash, row.UserId, row.SessionHash, input.ExpectedState, input.ExpectedUser, input.ExpectedSession, anchor)
		if out.Outcome == "consumed" {
			out.Outcome = "already_consumed"
		}
	}
	return rhandler.RecoveryAccept, nil
}

func singleState(input *Input) *LinkState {
	if input == nil || len(input.States) != 1 {
		return nil
	}
	return input.States[0]
}

func (input *Input) Init(context.Context) error {
	switch input.Mode {
	case "", "create", "consume", "replace":
		if singleState(input) == nil {
			return fmt.Errorf("one link state is required")
		}
		if input.Mode == "replace" && (strings.TrimSpace(input.ObservedState) == "" || strings.TrimSpace(input.Now) == "") {
			return fmt.Errorf("replacement state and clock are required")
		}
	case "cleanup":
		if _, err := time.Parse(stateTimeLayout, strings.TrimSpace(input.Before)); err != nil {
			return fmt.Errorf("cleanup horizon is required")
		}
		if input.ObservedState != "" || input.Now != "" {
			return fmt.Errorf("cleanup cannot apply replacement conditions")
		}
		for _, row := range input.States {
			if row == nil || strings.TrimSpace(row.FlowHash) == "" || !row.ShouldDelete {
				return fmt.Errorf("cleanup requires identified deletion rows")
			}
		}
	default:
		return fmt.Errorf("unsupported link state mutation mode")
	}
	return nil
}

func (h *Lifecycle) initReplace(entity *LinkState, state xhandler.LifecycleContext[LinkState, xhandler.NoParent, Output]) error {
	if entity.ShouldDelete {
		return fmt.Errorf("replacement cannot delete a link state")
	}
	if state.Previous == nil {
		return fmt.Errorf("replacement requires an existing flow")
	}
	if strings.TrimSpace(entity.FlowHash) == "" {
		return fmt.Errorf("replacement flow is required")
	}
	if entity.Has != nil && entity.Has.ExpiresAt {
		value, err := stateTime(entity.ExpiresAt)
		if err != nil {
			return err
		}
		entity.SetExpiresAt(value.Format(stateTimeLayout))
	}
	if entity.Has != nil && entity.Has.CreatedAt {
		value, err := stateTime(entity.CreatedAt)
		if err != nil {
			return err
		}
		entity.SetCreatedAt(value.Format(stateTimeLayout))
	}
	if entity.Has != nil && entity.Has.ConsumedAt && entity.ConsumedAt != nil {
		value, err := stateTime(*entity.ConsumedAt)
		if err != nil {
			return err
		}
		text := value.Format(stateTimeLayout)
		entity.SetConsumedAt(&text)
	}
	return nil
}

func (h *Lifecycle) initCleanup(entity *LinkState, state xhandler.LifecycleContext[LinkState, xhandler.NoParent, Output]) error {
	if !entity.ShouldDelete {
		return fmt.Errorf("cleanup requires explicit deletion")
	}
	if state.Previous == nil {
		return fmt.Errorf("cleanup row vanished before mutation")
	}
	before, err := time.Parse(stateTimeLayout, strings.TrimSpace(h.Input.Before))
	if err != nil {
		return fmt.Errorf("cleanup horizon is invalid")
	}
	expires, err := stateTime(state.Previous.ExpiresAt)
	if err != nil {
		return err
	}
	if expires.After(before) {
		return fmt.Errorf("cleanup row is no longer expired")
	}
	h.cleanupCount++
	if h.cleanupOldest.IsZero() || expires.Before(h.cleanupOldest) {
		h.cleanupOldest = expires
	}
	return nil
}
