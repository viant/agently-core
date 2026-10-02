package write

import (
	context "context"
	"fmt"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"strings"
	"time"
)

// Lifecycle customizes role Input.Token.
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

// Init keeps mutation modes on an existing composite identity. Missing rows
// are no-ops for clear/release/CAS instead of becoming incomplete inserts.
func (input *Input) Init(ctx context.Context) error {
	if input == nil {
		return nil
	}
	if input.Mode == "" {
		if input.LeaseMode != "" {
			return fmt.Errorf("token lease guard requires claim mode")
		}
		return nil
	}
	if input.Token == nil || strings.TrimSpace(input.Token.UserId) == "" || strings.TrimSpace(input.Token.Provider) == "" {
		return fmt.Errorf("token mutation requires user and provider")
	}
	switch input.Mode {
	case "clear":
	case "claim":
		if input.Has == nil || !input.Has.LeaseMode || input.LeaseMode != "claim" || input.Token.LeaseOwner == nil || strings.TrimSpace(*input.Token.LeaseOwner) == "" || input.Token.LeaseUntil == nil || input.Token.LeaseUntil.IsZero() {
			return fmt.Errorf("lease claim requires owner, expiration and native claim guard")
		}
	case "release":
		if input.Has == nil || !input.Has.ExpectedLeaseOwner || strings.TrimSpace(input.ExpectedLeaseOwner) == "" {
			return fmt.Errorf("lease release requires owner")
		}
	case "migrate":
		if input.Has == nil || !input.Has.ExpectedEncToken || input.ExpectedEncToken == "" || input.Token.EncToken == "" {
			return fmt.Errorf("ciphertext migration requires old and new ciphertext")
		}
	case "cas_put":
		if input.Has == nil || !input.Has.ExpectedVersion || !input.Has.ExpectedLeaseOwner || strings.TrimSpace(input.ExpectedLeaseOwner) == "" || input.Token.EncToken == "" {
			return fmt.Errorf("CAS put requires version, lease owner and ciphertext")
		}
	default:
		return fmt.Errorf("unsupported token mutation mode %q", input.Mode)
	}
	if input.Mode != "claim" && input.LeaseMode != "" {
		return fmt.Errorf("token lease guard is only valid for claim")
	}
	indexes, err := input.ReadIndexes(ctx)
	if err != nil {
		return err
	}
	key := WriterHandlerCurrentWriterKey{UserId: input.Token.UserId, Provider: input.Token.Provider}
	if !indexes.CurrentWriterByKey.Has(key) {
		input.Token = nil
	}
	return nil
}

func (hooks *Lifecycle) Init(ctx context.Context, entity *Token, state xhandler.LifecycleContext[Token, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if hooks.Input != nil && hooks.Input.Mode != "" {
		if state.Previous == nil {
			return fmt.Errorf("token mutation requires an existing row")
		}
		// The mutation modes author only their own fields; the original token
		// upsert's forced presence remains limited to the default mode below.
		entity.Has = &TokenHas{UserId: true, Provider: true}
		switch hooks.Input.Mode {
		case "claim":
			owner := strings.TrimSpace(*entity.LeaseOwner)
			until := entity.LeaseUntil.UTC()
			entity.SetLeaseOwner(&owner)
			entity.SetLeaseUntil(&until)
			entity.SetRefreshStatus("refreshing")
		case "clear":
			entity.SetEncToken("")
			entity.SetVersion(state.Previous.Version + 1)
			entity.SetLeaseOwner(nil)
			entity.SetLeaseUntil(nil)
			entity.SetRefreshStatus("idle")
			now := time.Now().UTC()
			entity.SetUpdatedAt(&now)
		case "release":
			entity.SetLeaseOwner(nil)
			entity.SetLeaseUntil(nil)
			entity.SetRefreshStatus("idle")
		case "migrate":
			entity.SetEncToken(entity.EncToken)
			entity.SetVersion(state.Previous.Version + 1)
			now := time.Now().UTC()
			entity.SetUpdatedAt(&now)
		case "cas_put":
			entity.SetEncToken(entity.EncToken)
			entity.SetVersion(state.Previous.Version + 1)
			entity.SetLeaseOwner(nil)
			entity.SetLeaseUntil(nil)
			entity.SetRefreshStatus("idle")
			now := time.Now().UTC()
			entity.SetUpdatedAt(&now)
		}
		return nil
	}
	now := time.Now().UTC()
	if state.Previous == nil {
		entity.SetCreatedAt(now)
		return nil
	}
	entity.SetUserId(entity.UserId)
	entity.SetProvider(entity.Provider)
	entity.SetEncToken(entity.EncToken)
	if entity.UpdatedAt == nil {
		entity.SetUpdatedAt(&now)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *Token, state xhandler.LifecycleContext[Token, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *Token, state xhandler.LifecycleContext[Token, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *Token, state xhandler.LifecycleContext[Token, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
