package executionprotection

import (
	"context"
	"fmt"
	"reflect"
	"time"

	claimwrite "github.com/viant/agently-core/internal/datly/toolexecutionclaim/write"
	claimstore "github.com/viant/agently-core/internal/store/toolexecutionclaim"
	toolprotection "github.com/viant/agently-core/protocol/tool/protection"
	dexec "github.com/viant/datly/exec"
)

type componentRepository struct{ store *claimstore.Store }

type unavailableRepository struct{ reason string }

// NewComponentRepository uses the shared linked Datly host. Missing persistence
// is reported when a protected tool attempts to claim or finish.
func NewComponentRepository(invoker dexec.ComponentInvoker) Repository {
	if invoker == nil {
		return &unavailableRepository{reason: "standard agently component host is nil"}
	}
	value := reflect.ValueOf(invoker)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return &unavailableRepository{reason: "standard agently component host is nil"}
	}
	return &componentRepository{store: &claimstore.Store{Invoker: invoker}}
}

func (r *unavailableRepository) Claim(context.Context, ClaimRecord) (bool, error) {
	return false, fmt.Errorf("claim repository unavailable: %s", r.reason)
}

func (r *unavailableRepository) Finish(context.Context, string, toolprotection.State, time.Time) error {
	return fmt.Errorf("claim repository unavailable: %s", r.reason)
}

func (r *componentRepository) Claim(ctx context.Context, record ClaimRecord) (bool, error) {
	claim := &claimwrite.Claim{}
	claim.SetClaimKey(record.ClaimKey)
	claim.SetRuleId(record.RuleID)
	claim.SetCanonicalToolName(record.CanonicalToolName)
	claim.SetTurnId(record.TurnID)
	claim.SetSemanticRequestHash(record.SemanticHash)
	claim.SetState("claimed")
	claim.SetCreatedAt(&record.CreatedAt)
	claim.SetUpdatedAt(&record.CreatedAt)
	return r.store.Claim(ctx, claim)
}

func (r *componentRepository) Finish(ctx context.Context, claimKey string, state toolprotection.State, finishedAt time.Time) error {
	return r.store.Finish(ctx, claimKey, string(state), finishedAt)
}
