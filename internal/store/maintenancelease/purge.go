package maintenancelease

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	read "github.com/viant/agently-core/internal/datly/maintenancelease/read"
	write "github.com/viant/agently-core/internal/datly/maintenancelease/write"
	dexec "github.com/viant/datly/exec"
	custom "github.com/viant/datly/runtime/handler/custom"
	"github.com/viant/datly/spec"
	"github.com/viant/x"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

const expiredRetention = 7 * 24 * time.Hour

var ErrLeaseLost = errors.New("maintenance lease is no longer owned by this worker")

type PurgeComponent struct {
	Contract xdatly.Component[PurgeInput, PurgeOutput] `component:"MaintenanceLeasePurge,path=/v1/internal/agently/maintenance-lease/purge,method=POST,handler=NewPurge,internal=true"`
}

type PurgeInput struct {
	Key     string `parameter:"Key,kind=body,in=key,required=true"`
	OwnerID string `parameter:"OwnerID,kind=body,in=ownerId,required=true"`
	Token   string `parameter:"Token,kind=body,in=token,required=true"`
}

type PurgeOutput struct {
	Deleted int64 `json:"deleted"`
}

type Purge struct{}

func NewPurge() handler.Contract[PurgeInput, PurgeOutput] { return &Purge{} }

func Exports() (*x.Registry, error) {
	registry := x.NewRegistry()
	for _, typ := range []reflect.Type{reflect.TypeFor[PurgeInput](), reflect.TypeFor[PurgeOutput]()} {
		registry.Register(x.NewType(typ))
	}
	factory, err := x.NewFunction(reflect.TypeFor[PurgeComponent]().PkgPath(), "NewPurge", custom.Factory(NewPurge))
	if err != nil {
		return nil, err
	}
	if err := registry.RegisterFunctions(factory); err != nil {
		return nil, err
	}
	return registry, nil
}

var _ handler.Contract[PurgeInput, PurgeOutput] = (*Purge)(nil)

func (*Purge) Exec(ctx context.Context, session handler.Session, input *PurgeInput, output *PurgeOutput) error {
	if session == nil || session.Binder() == nil || input == nil || output == nil {
		return fmt.Errorf("maintenance purge invocation is incomplete")
	}
	key, owner, token := strings.TrimSpace(input.Key), strings.TrimSpace(input.OwnerID), strings.TrimSpace(input.Token)
	if key == "" || owner == "" || token == "" {
		return ErrInvalidLease
	}
	deps := struct {
		Invoker dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
		Starter handler.TransactionStarter `bind:"kind=transactionStarter,required"`
	}{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil || deps.Starter == nil {
		return fmt.Errorf("maintenance purge capabilities are unavailable")
	}
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	store := &Store{Invoker: deps.Invoker}
	now, err := store.Fence(ctx, Lease{Key: key, OwnerID: owner, Token: token})
	if err != nil {
		return err
	}
	cutoff := now.Add(-expiredRetention)
	query := &read.Input{}
	query.SetExpiresBefore(cutoff)
	rows, err := store.snapshot(ctx, query)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row == nil || row.LeaseKey == nil {
			continue
		}
		if row.LeaseToken == nil {
			return fmt.Errorf("expired maintenance lease %q lacks token", *row.LeaseKey)
		}
		if *row.LeaseKey == key {
			continue
		}
		entity := &write.Lease{}
		entity.SetLeaseKey(*row.LeaseKey)
		entity.SetShouldDelete(true)
		mutation := &write.Input{}
		mutation.SetMode("delete")
		mutation.SetExpectedToken(*row.LeaseToken)
		mutation.SetExpiresBefore(cutoff)
		mutation.SetLeases([]*write.Lease{entity})
		if err := store.patch(ctx, mutation); err != nil {
			return err
		}
		output.Deleted++
	}
	return nil
}

var purgeTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[PurgeComponent]().PkgPath(), Name: "MaintenanceLeasePurge"},
	Route:     spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/maintenance-lease/purge"},
}

func (s *Store) DeleteExpired(ctx context.Context, lease Lease) (int64, error) {
	lease = normalize(lease)
	if !valid(lease) {
		return 0, ErrInvalidLease
	}
	if s == nil || s.Invoker == nil {
		return 0, fmt.Errorf("maintenance lease component invoker is required")
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: purgeTarget, Input: &PurgeInput{Key: lease.Key, OwnerID: lease.OwnerID, Token: lease.Token}})
	if err != nil {
		return 0, err
	}
	out, ok := value.(*PurgeOutput)
	if !ok || out == nil {
		return 0, fmt.Errorf("maintenance purge returned %T", value)
	}
	return out.Deleted, nil
}
