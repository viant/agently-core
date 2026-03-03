package store

import (
	"context"

	old "github.com/viant/agently-core/app/store/data"
	"github.com/viant/datly"
	hstate "github.com/viant/xdatly/handler/state"
)

var ErrPermissionDenied = old.ErrPermissionDenied

type Service = old.Service
type Direction = old.Direction
type PageInput = old.PageInput
type ConversationPage = old.ConversationPage
type MessagePage = old.MessagePage
type TurnPage = old.TurnPage
type RunStepPage = old.RunStepPage
type Option = old.Option

const (
	DirectionBefore = old.DirectionBefore
	DirectionAfter  = old.DirectionAfter
	DirectionLatest = old.DirectionLatest
)

func WithQuerySelector(selectors ...*hstate.NamedQuerySelector) Option {
	return old.WithQuerySelector(selectors...)
}
func WithPrincipal(userID string) Option      { return old.WithPrincipal(userID) }
func WithAdminPrincipal(userID string) Option { return old.WithAdminPrincipal(userID) }

func NewService(dao *datly.Service) Service { return old.NewService(dao) }
func NewDatly(ctx context.Context) (*datly.Service, error) {
	return old.NewDatly(ctx)
}
func NewDatlyServiceFromEnv(ctx context.Context) (*datly.Service, error) {
	return old.NewDatlyServiceFromEnv(ctx)
}
func NewDatlyInMemory(ctx context.Context) (*datly.Service, error) { return old.NewDatlyInMemory(ctx) }
func NewThinServiceFromEnv(ctx context.Context) (Service, error) {
	return old.NewThinServiceFromEnv(ctx)
}
func NewThinServiceInMemory(ctx context.Context) (Service, error) {
	return old.NewThinServiceInMemory(ctx)
}
