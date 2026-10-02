package write

import (
	context "context"
	"errors"
	"fmt"

	"github.com/viant/agently-core/internal/datly/invariant"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"strings"
	"time"
)

var ErrAlreadyClaimed = errors.New("tool execution claim already exists")
var ErrClaimMissing = errors.New("tool execution claim is missing")

// Lifecycle customizes role Input.Claims.
type Lifecycle struct {
	Input *Input `bind:"kind=input"`
}

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *Claim, state xhandler.LifecycleContext[Claim, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if hooks.Input == nil || !hooks.Input.Trusted {
		return fmt.Errorf("trusted claim access is required")
	}
	if strings.TrimSpace(entity.ClaimKey) == "" {
		return fmt.Errorf("claim key is required")
	}
	if hooks.Input.OrphanDelete {
		if hooks.Input.ClaimMode != "" {
			return fmt.Errorf("maintenance modes are mutually exclusive")
		}
		return invariant.ValidateOrphanDelete(entity, state.Previous, "ClaimKey")
	}
	if entity.ShouldDelete {
		if strings.TrimSpace(hooks.Input.ExpectedTurnID) == "" {
			return fmt.Errorf("claim deletion requires expected turn")
		}
		return nil
	}
	switch hooks.Input.ClaimMode {
	case "", "claim", "finish":
	default:
		return fmt.Errorf("invalid claim mutation mode %q", hooks.Input.ClaimMode)
	}
	if hooks.Input.ClaimMode == "claim" && state.Previous != nil {
		return ErrAlreadyClaimed
	}
	if hooks.Input.ClaimMode == "finish" && state.Previous == nil {
		return ErrClaimMissing
	}
	if entity.Has != nil && entity.Has.State || state.Previous == nil {
		switch entity.State {
		case "claimed", "completed", "failed", "unknown":
		default:
			return fmt.Errorf("invalid claim state %q", entity.State)
		}
	}
	if state.Previous == nil {
		for name, value := range map[string]string{
			"ruleId": entity.RuleId, "canonicalToolName": entity.CanonicalToolName,
			"turnId": entity.TurnId, "semanticRequestHash": entity.SemanticRequestHash,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s is required", name)
			}
		}
		now := time.Now().UTC()
		if entity.CreatedAt == nil {
			entity.SetCreatedAt(&now)
		}
		if entity.UpdatedAt == nil {
			entity.SetUpdatedAt(&now)
		}
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *Claim, state xhandler.LifecycleContext[Claim, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *Claim, state xhandler.LifecycleContext[Claim, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *Claim, state xhandler.LifecycleContext[Claim, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
