package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/authlog"
	linkstate "github.com/viant/agently-core/pkg/agently/user/oauth/linkstate"
	linkconsume "github.com/viant/agently-core/pkg/agently/user/oauth/linkstate/consume"
	linkcleanup "github.com/viant/agently-core/pkg/agently/user/oauth/linkstate/deleteexpired"
	linkwrite "github.com/viant/agently-core/pkg/agently/user/oauth/linkstate/write"
	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"
)

// ErrOAuthStateInvalid is the single non-enumerable failure returned for every
// rejected state consume: absent, expired, replayed, cross-user and
// cross-session states are indistinguishable to callers. The classification is
// recorded in audit logs only.
var ErrOAuthStateInvalid = errors.New("oauth link state is invalid")

// OAuthStateRecord is the non-secret oauth_link_state row. It never carries
// authorization codes, PKCE verifiers, client secrets or tokens; the encrypted
// state blob itself stays with the flow owner (browser round-trip) only.
type OAuthStateRecord struct {
	StateHash       string
	FlowHash        string
	CanonicalUserID string
	SessionHash     string
	Provider        string
	ExpiresAt       time.Time
	ConsumedAt      *time.Time
	CreatedAt       time.Time
}

// Pending reports whether the record is unconsumed and unexpired at now.
func (r *OAuthStateRecord) Pending(now time.Time) bool {
	if r == nil || r.ConsumedAt != nil {
		return false
	}
	return r.ExpiresAt.After(now)
}

// OAuthStateStore is the distributed single-use OAuth state surface used by
// the MCP link endpoints. Implementations must provide cross-pod
// CreateOrGetPending deduplication and an atomic pending-to-consumed
// transition; encryption alone does not make state single-use.
type OAuthStateStore interface {
	// CreateOrGetPending returns the stored flow row and whether this call
	// created (or replaced a consumed/expired) row. The creator owns the flow
	// and receives the authorization URL; concurrent callers must poll.
	CreateOrGetPending(ctx context.Context, record *OAuthStateRecord) (stored *OAuthStateRecord, created bool, err error)
	// Consume atomically transitions the state to consumed. It fails with
	// ErrOAuthStateInvalid when the record is absent, expired, already
	// consumed, owned by another canonical user or bound to another session.
	Consume(ctx context.Context, stateHash, canonicalUserID, sessionHash string) error
	// DeleteExpired removes rows expired at or before the horizon and reports
	// the deleted count plus the oldest removed expiry for metrics.
	DeleteExpired(ctx context.Context, before time.Time) (deleted int64, oldestExpiresAt time.Time, err error)
}

// OAuthStateStoreDatly implements OAuthStateStore exclusively through the
// registered Datly linkstate components: no raw database/sql access happens
// here or in any HTTP handler above it.
type OAuthStateStoreDatly struct {
	dao *datly.Service
	now func() time.Time
}

// NewOAuthStateStoreDatly creates the Datly-backed state store adapter.
func NewOAuthStateStoreDatly(dao *datly.Service) *OAuthStateStoreDatly {
	if dao == nil {
		return nil
	}
	return &OAuthStateStoreDatly{dao: dao, now: time.Now}
}

// DefineOAuthLinkStateComponents registers the read, create-or-get-pending,
// atomic consume and delete-expired linkstate components with datly.Service.
// Called during auth runtime initialization.
func DefineOAuthLinkStateComponents(ctx context.Context, dao *datly.Service) error {
	if dao == nil {
		return nil
	}
	if err := linkstate.DefineLinkStateComponent(ctx, dao); err != nil {
		return fmt.Errorf("failed to register oauth link state read component: %w", err)
	}
	if _, err := linkwrite.DefineComponent(ctx, dao); err != nil {
		return fmt.Errorf("failed to register oauth link state write component: %w", err)
	}
	if _, err := linkconsume.DefineComponent(ctx, dao); err != nil {
		return fmt.Errorf("failed to register oauth link state consume component: %w", err)
	}
	if _, err := linkcleanup.DefineComponent(ctx, dao); err != nil {
		return fmt.Errorf("failed to register oauth link state cleanup component: %w", err)
	}
	return nil
}

func (s *OAuthStateStoreDatly) CreateOrGetPending(ctx context.Context, record *OAuthStateRecord) (*OAuthStateRecord, bool, error) {
	if s == nil || s.dao == nil {
		return nil, false, fmt.Errorf("oauth state store is not configured")
	}
	if record == nil {
		return nil, false, fmt.Errorf("oauth state record is required")
	}
	now := s.now().UTC()
	state := &linkwrite.LinkState{}
	state.SetStateHash(strings.TrimSpace(record.StateHash))
	state.SetFlowHash(strings.TrimSpace(record.FlowHash))
	state.SetUserID(strings.TrimSpace(record.CanonicalUserID))
	state.SetSessionHash(strings.TrimSpace(record.SessionHash))
	state.SetProvider(strings.TrimSpace(record.Provider))
	state.SetExpiresAt(record.ExpiresAt.UTC().Format(linkstate.TimeLayout))
	state.SetNow(now.Format(linkstate.TimeLayout))
	in := &linkwrite.Input{State: state}
	out := &linkwrite.Output{}
	if _, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath("PATCH", linkwrite.PathURI)),
		datly.WithInput(in),
		datly.WithOutput(out)); err != nil {
		return nil, false, err
	}
	stored, err := recordFromWrite(out.Data)
	if err != nil {
		return nil, false, err
	}
	if stored == nil {
		return nil, false, fmt.Errorf("oauth state store returned no row")
	}
	return stored, out.Created, nil
}

func (s *OAuthStateStoreDatly) Consume(ctx context.Context, stateHash, canonicalUserID, sessionHash string) error {
	if s == nil || s.dao == nil {
		return fmt.Errorf("oauth state store is not configured")
	}
	in := &linkconsume.Input{Consume: &linkconsume.Consume{
		StateHash:       strings.TrimSpace(stateHash),
		CanonicalUserID: strings.TrimSpace(canonicalUserID),
		SessionHash:     strings.TrimSpace(sessionHash),
		Now:             s.now().UTC().Format(linkstate.TimeLayout),
	}}
	out := &linkconsume.Output{}
	if _, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath("POST", linkconsume.PathURI)),
		datly.WithInput(in),
		datly.WithOutput(out)); err != nil {
		return err
	}
	if out.Outcome == linkconsume.OutcomeConsumed {
		return nil
	}
	// Audit the classification without exposing it: callers observe one
	// non-enumerable failure for every rejected transition.
	authlog.Log(ctx, authlog.Event{
		Op:             "mcp_auth_state_consume_rejected",
		UserID:         strings.TrimSpace(canonicalUserID),
		Classification: strings.TrimSpace(out.Outcome),
		Action:         "reject",
	})
	return ErrOAuthStateInvalid
}

func (s *OAuthStateStoreDatly) DeleteExpired(ctx context.Context, before time.Time) (int64, time.Time, error) {
	if s == nil || s.dao == nil {
		return 0, time.Time{}, fmt.Errorf("oauth state store is not configured")
	}
	in := &linkcleanup.Input{Cleanup: &linkcleanup.Cleanup{
		Before: before.UTC().Format(linkstate.TimeLayout),
	}}
	out := &linkcleanup.Output{}
	if _, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath("DELETE", linkcleanup.PathURI)),
		datly.WithInput(in),
		datly.WithOutput(out)); err != nil {
		return 0, time.Time{}, err
	}
	oldest := time.Time{}
	if strings.TrimSpace(out.OldestExpiresAt) != "" {
		if parsed, err := time.Parse(linkstate.TimeLayout, strings.TrimSpace(out.OldestExpiresAt)); err == nil {
			oldest = parsed.UTC()
		}
	}
	return out.Deleted, oldest, nil
}

// GetPending returns the unexpired pending record for a flow hash (status
// polling); nil when none exists.
func (s *OAuthStateStoreDatly) GetPending(ctx context.Context, flowHash string) (*OAuthStateRecord, error) {
	if s == nil || s.dao == nil {
		return nil, fmt.Errorf("oauth state store is not configured")
	}
	in := &linkstate.LinkStateInput{
		FlowHash: strings.TrimSpace(flowHash),
		Has: &linkstate.LinkStateInputHas{
			FlowHash: true,
		},
	}
	out := &linkstate.LinkStateOutput{}
	if _, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath("GET", linkstate.LinkStatePathURI)),
		datly.WithInput(in),
		datly.WithOutput(out)); err != nil {
		return nil, err
	}
	for _, item := range out.Data {
		record, err := recordFromView(item)
		if err != nil {
			return nil, err
		}
		if record.Pending(s.now().UTC()) {
			return record, nil
		}
	}
	return nil, nil
}

func recordFromWrite(state *linkwrite.LinkState) (*OAuthStateRecord, error) {
	if state == nil {
		return nil, nil
	}
	expiresAt, err := parseLinkStateTime(state.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("oauth state row has invalid expiry: %w", err)
	}
	record := &OAuthStateRecord{
		StateHash:       strings.TrimSpace(state.StateHash),
		FlowHash:        strings.TrimSpace(state.FlowHash),
		CanonicalUserID: strings.TrimSpace(state.UserID),
		SessionHash:     strings.TrimSpace(state.SessionHash),
		Provider:        strings.TrimSpace(state.Provider),
		ExpiresAt:       expiresAt,
	}
	if state.ConsumedAt != nil && strings.TrimSpace(*state.ConsumedAt) != "" {
		if consumed, err := parseLinkStateTime(*state.ConsumedAt); err == nil {
			record.ConsumedAt = &consumed
		}
	}
	if created, err := parseLinkStateTime(state.CreatedAt); err == nil {
		record.CreatedAt = created
	}
	return record, nil
}

func recordFromView(view *linkstate.LinkStateView) (*OAuthStateRecord, error) {
	if view == nil {
		return nil, nil
	}
	if view.ExpiresAt.IsZero() {
		return nil, fmt.Errorf("oauth state row has invalid expiry: empty timestamp")
	}
	record := &OAuthStateRecord{
		StateHash:       strings.TrimSpace(view.StateHash),
		FlowHash:        strings.TrimSpace(view.FlowHash),
		CanonicalUserID: strings.TrimSpace(view.UserId),
		SessionHash:     strings.TrimSpace(view.SessionHash),
		Provider:        strings.TrimSpace(view.Provider),
		ExpiresAt:       view.ExpiresAt.UTC(),
	}
	if view.ConsumedAt != nil && !view.ConsumedAt.IsZero() {
		consumed := view.ConsumedAt.UTC()
		record.ConsumedAt = &consumed
	}
	if !view.CreatedAt.IsZero() {
		record.CreatedAt = view.CreatedAt.UTC()
	}
	return record, nil
}

func parseLinkStateTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	if parsed, err := time.Parse(linkstate.TimeLayout, value); err == nil {
		return parsed.UTC(), nil
	}
	// Some drivers append fractional seconds or a timezone; accept RFC3339 too.
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp %q", value)
}
