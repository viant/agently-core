package write

import (
	context "context"
	"errors"
	"fmt"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"strings"
	"time"
)

// Lifecycle customizes role Input.Leases.
type Lifecycle struct {
	Input *Input `bind:"kind=input"`
}

var (
	ErrNotFound = errors.New("maintenance lease not found")
	ErrConflict = errors.New("maintenance lease changed")
)

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *Lease, state xhandler.LifecycleContext[Lease, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if hooks.Input == nil || !hooks.Input.Trusted {
		return fmt.Errorf("trusted maintenance lease access is required")
	}
	if strings.TrimSpace(entity.LeaseKey) == "" {
		return fmt.Errorf("maintenance lease key is required")
	}
	switch hooks.Input.Mode {
	case "create":
		if entity.ShouldDelete || state.Previous != nil {
			return ErrConflict
		}
		if strings.TrimSpace(entity.OwnerId) == "" || strings.TrimSpace(entity.LeaseToken) == "" || entity.LeaseUntil.IsZero() {
			return fmt.Errorf("owner, token and expiry are required")
		}
	case "claim":
		if entity.ShouldDelete || state.Previous == nil {
			return ErrNotFound
		}
		if hooks.Input.Has == nil || !hooks.Input.Has.ExpiresBefore || strings.TrimSpace(hooks.Input.ExpectedToken) == "" {
			return fmt.Errorf("claim requires expiry and prior token guards")
		}
		if strings.TrimSpace(entity.OwnerId) == "" || strings.TrimSpace(entity.LeaseToken) == "" || entity.LeaseUntil.IsZero() {
			return fmt.Errorf("owner, token and expiry are required")
		}
	case "renew":
		if entity.ShouldDelete || state.Previous == nil {
			return ErrNotFound
		}
		if hooks.Input.Has == nil || !hooks.Input.Has.LiveAfter || strings.TrimSpace(hooks.Input.ExpectedOwner) == "" || strings.TrimSpace(hooks.Input.ExpectedToken) == "" {
			return fmt.Errorf("renew requires live owner and token guards")
		}
		if entity.LeaseUntil.IsZero() {
			return fmt.Errorf("renewed expiry is required")
		}
	case "release":
		if entity.ShouldDelete || state.Previous == nil {
			return ErrNotFound
		}
		if strings.TrimSpace(hooks.Input.ExpectedOwner) == "" || strings.TrimSpace(hooks.Input.ExpectedToken) == "" || entity.LeaseUntil.IsZero() {
			return fmt.Errorf("release requires owner, token and database clock")
		}
	case "guard":
		if entity.ShouldDelete || state.Previous == nil {
			return ErrNotFound
		}
		if hooks.Input.Has == nil || !hooks.Input.Has.LiveAfter || strings.TrimSpace(hooks.Input.ExpectedOwner) == "" || strings.TrimSpace(hooks.Input.ExpectedToken) == "" || entity.UpdatedAt.IsZero() {
			return fmt.Errorf("guard requires live owner, token and database clock")
		}
		// The parent cleanup transaction must hold this row's write lock until
		// its child deletions commit. Force a real update even when the server
		// clock has the same stored precision as the previous timestamp.
		if entity.UpdatedAt.Equal(state.Previous.UpdatedAt) {
			entity.SetUpdatedAt(entity.UpdatedAt.Add(time.Second))
		}
	case "delete":
		if !entity.ShouldDelete || hooks.Input.Has == nil || !hooks.Input.Has.ExpiresBefore || strings.TrimSpace(hooks.Input.ExpectedToken) == "" {
			return fmt.Errorf("delete requires marker, expiry and prior token")
		}
	default:
		return fmt.Errorf("unsupported maintenance lease mode %q", hooks.Input.Mode)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *Lease, state xhandler.LifecycleContext[Lease, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *Lease, state xhandler.LifecycleContext[Lease, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *Lease, state xhandler.LifecycleContext[Lease, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
