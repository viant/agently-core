package store

import (
	"context"

	old "github.com/viant/agently-core/app/store/data"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/standalone"
	hstate "github.com/viant/xdatly/state"
)

var ErrPermissionDenied = old.ErrPermissionDenied
var ErrConversationNotFound = old.ErrConversationNotFound
var ErrConversationActive = old.ErrConversationActive
var ErrConversationNonTerminal = old.ErrConversationNonTerminal
var ErrConversationGraphReferenced = old.ErrConversationGraphReferenced
var ErrConversationGraphTooLarge = old.ErrConversationGraphTooLarge
var ErrConversationScheduleReferenced = old.ErrConversationScheduleReferenced

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

func WithQuerySelector(selectors ...*hstate.NamedSelector) Option {
	return old.WithQuerySelector(selectors...)
}
func WithPrincipal(userID string) Option      { return old.WithPrincipal(userID) }
func WithAdminPrincipal(userID string) Option { return old.WithAdminPrincipal(userID) }

type ServiceOption = old.ServiceOption

func WithWriteGate(key string) ServiceOption { return old.WithWriteGate(key) }
func NewService(invoker dexec.ComponentInvoker, opts ...ServiceOption) Service {
	return old.NewService(invoker, opts...)
}
func NewRuntime(ctx context.Context) (*standalone.Server, error) { return old.NewRuntime(ctx) }
func NewRuntimeFromWorkspace(ctx context.Context, root string) (*standalone.Server, error) {
	return old.NewRuntimeFromWorkspace(ctx, root)
}
func NewRuntimeInMemory(ctx context.Context) (*standalone.Server, error) {
	return old.NewRuntimeInMemory(ctx)
}
func NewThinServiceFromEnv(ctx context.Context) (Service, error) {
	return old.NewThinServiceFromEnv(ctx)
}
func NewThinServiceInMemory(ctx context.Context) (Service, error) {
	return old.NewThinServiceInMemory(ctx)
}

func CloseService(ctx context.Context, service Service) error { return old.CloseService(ctx, service) }
