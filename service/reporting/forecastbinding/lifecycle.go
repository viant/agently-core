package forecastbinding

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/evidence"
)

type AdmissionStore interface {
	PlanStore
	SaveAdmission(context.Context, Admission, string) error
}

// Factory is an explicit host dependency, not enabled by package registration.
// It captures only client.forecastIntent; routing hints cannot become intent.
type Factory struct {
	runtime       *Runtime
	store         AdmissionStore
	zone          string
	rolloutCutoff time.Time
	restoreOnly   bool
}

type FactoryOption func(*Factory) error

// WithRolloutCutoff pins legacy-resume eligibility to deployment configuration.
// The cutoff is never inferred from boot time or missing evidence.
func WithRolloutCutoff(cutoff time.Time) FactoryOption {
	return func(factory *Factory) error {
		if cutoff.IsZero() {
			return reject("rollout cutoff is required")
		}
		factory.rolloutCutoff = cutoff
		return nil
	}
}

// WithRestoreOnly keeps persisted authority enforceable after opt-in removal,
// while admitting no new turns or current caller intent.
func WithRestoreOnly() FactoryOption {
	return func(factory *Factory) error { factory.restoreOnly = true; return nil }
}

type OwnedRunCreatedAtReader interface {
	OwnedRunCreatedAt(context.Context, Scope) (time.Time, error)
}

func NewFactory(producer PolicyProducer, store AdmissionStore, zone string, options ...FactoryOption) (*Factory, error) {
	if _, err := time.LoadLocation(zone); err != nil {
		return nil, err
	}
	runtime, err := NewRuntime(producer, store)
	if err != nil {
		return nil, err
	}
	factory := &Factory{runtime: runtime, store: store, zone: zone}
	for _, option := range options {
		if option != nil {
			if err := option(factory); err != nil {
				return nil, err
			}
		}
	}
	return factory, nil
}

type pendingAdmission struct {
	factory  *Factory
	raw      json.RawMessage
	received time.Time
	owner    string
}

func (f *Factory) Capture(ctx context.Context, input evidence.Input) (evidence.Pending, error) {
	if f.restoreOnly {
		return nil, nil
	}
	var raw json.RawMessage
	if !input.Nested && len(input.Context) > 0 && !bytes.Equal(bytes.TrimSpace(input.Context), []byte("null")) {
		var contextObject map[string]json.RawMessage
		if err := json.Unmarshal(input.Context, &contextObject); err != nil {
			return nil, err
		}
		if client, ok := contextObject["client"]; ok {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(client, &fields); err != nil {
				return nil, reject("invalid client context")
			}
			raw = append(json.RawMessage(nil), fields["forecastIntent"]...)
		}
	}
	// Validate syntax and explicit intent before intake. These temporary scope
	// sentinels are never saved; Begin supplies the actual durable native scope.
	if _, err := AdmitUserContext(ctx, "pending", "pending", "pending", input.ReceivedAt, f.zone, raw); err != nil {
		return nil, err
	}
	return &pendingAdmission{factory: f, raw: raw, received: input.ReceivedAt, owner: authctx.EffectiveUserID(ctx)}, nil
}

func (p *pendingAdmission) Begin(ctx context.Context, turn evidence.Turn) (context.Context, error) {
	if authctx.EffectiveUserID(ctx) != p.owner {
		return ctx, reject("admission principal changed")
	}
	admission, err := AdmitUserContext(ctx, turn.ConversationID, turn.TurnID, turn.StarterMessageID, p.received, p.factory.zone, p.raw)
	if err != nil {
		return ctx, err
	}
	if turn.LeaseOwner == nil {
		return ctx, reject("admission execution lease missing")
	}
	if err = p.factory.store.SaveAdmission(ctx, admission, turn.LeaseOwner()); err != nil {
		return ctx, err
	}
	confirmed, err := p.factory.store.LoadAdmission(ctx, admission.Scope)
	if err != nil {
		return ctx, err
	}
	expected, _ := json.Marshal(admission)
	actual, _ := json.Marshal(confirmed)
	if !bytes.Equal(expected, actual) {
		return ctx, reject("admission save unconfirmed")
	}
	return p.factory.install(ctx, turn, admission.Scope)
}

func (f *Factory) Restore(ctx context.Context, turn evidence.Turn) (context.Context, error) {
	scope := Scope{OwnerID: authctx.EffectiveUserID(ctx), ConversationID: turn.ConversationID, TurnID: turn.TurnID}
	admission, err := f.store.LoadAdmission(ctx, scope)
	if err != nil {
		return ctx, err
	}
	if admission == nil {
		reader, ok := f.store.(OwnedRunCreatedAtReader)
		if !ok {
			return ctx, reject("restored admission missing")
		}
		createdAt, err := reader.OwnedRunCreatedAt(ctx, scope)
		if err != nil {
			return ctx, err
		}
		if createdAt.IsZero() {
			return ctx, reject("server run creation time missing")
		}
		if f.restoreOnly {
			return ctx, nil
		}
		if f.rolloutCutoff.IsZero() || createdAt.IsZero() || !createdAt.Before(f.rolloutCutoff) {
			return ctx, reject("post-rollout admission missing")
		}
		return ctx, nil
	}
	if admission.Scope != scope {
		return ctx, reject("restored admission scope mismatch")
	}
	if err = admission.Validate(); err != nil {
		return ctx, err
	}
	// No new clock, caller context, or policy state is admitted during resume.
	controller, err := NewController(f.runtime, scope, turn.LeaseOwner)
	if err != nil {
		return ctx, err
	}
	if err = controller.RestorePlans(ctx); err != nil {
		return ctx, err
	}
	return evidence.WithReportCommands(evidence.WithPublication(evidence.WithTools(ctx, controller), controller.publication), controller), nil
}

func (f *Factory) install(ctx context.Context, turn evidence.Turn, scope Scope) (context.Context, error) {
	controller, err := NewController(f.runtime, scope, turn.LeaseOwner)
	if err != nil {
		return ctx, err
	}
	if err = controller.check(ctx); err != nil {
		return ctx, err
	}
	return evidence.WithReportCommands(evidence.WithPublication(evidence.WithTools(ctx, controller), controller.publication), controller), nil
}
