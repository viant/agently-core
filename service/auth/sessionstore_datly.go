package auth

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	read "github.com/viant/agently-core/internal/datly/session/read"
	write "github.com/viant/agently-core/internal/datly/session/write"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

var sessionReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/user/session"},
}

var sessionWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/user/session"},
}

// SessionStoreNative preserves the session domain contract through the linked
// Datly v1 reader and writer. The reader owns the users join used for display
// identity, and the writer owns sparse updates and idempotent deletion.
type SessionStoreNative struct {
	invoker dexec.ComponentInvoker
}

func NewSessionStoreNative(invoker dexec.ComponentInvoker) *SessionStoreNative {
	if invoker == nil {
		return nil
	}
	return &SessionStoreNative{invoker: invoker}
}

func (s *SessionStoreNative) Get(ctx context.Context, id string) (*SessionRecord, error) {
	if s == nil || s.invoker == nil || strings.TrimSpace(id) == "" {
		return nil, nil
	}
	started := time.Now()
	var opErr error
	defer func() { logDatlyStoreOp(ctx, "session", "get", strings.TrimSpace(id), started, opErr) }()
	input := &read.SessionInput{}
	input.SetId(strings.TrimSpace(id))
	value, err := s.invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: sessionReaderTarget, Input: input})
	if err != nil {
		opErr = err
		return nil, err
	}
	out, ok := value.(*read.SessionOutput)
	if !ok || out == nil {
		opErr = fmt.Errorf("session reader returned %T", value)
		return nil, opErr
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	view := out.Data[0]
	if view == nil {
		opErr = fmt.Errorf("session reader returned a nil row")
		return nil, opErr
	}
	rec := &SessionRecord{
		ID: view.Id, UserID: view.UserId, Provider: view.Provider,
		CreatedAt: view.CreatedAt, ExpiresAt: view.ExpiresAt,
		Subject:  strings.TrimSpace(firstNonEmpty(derefSession(view.Subject), view.UserId)),
		Username: strings.TrimSpace(firstNonEmpty(derefSession(view.DisplayName), derefSession(view.Username), derefSession(view.Email), view.UserId)),
		Email:    strings.TrimSpace(derefSession(view.Email)),
	}
	return rec, nil
}

func derefSession(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *SessionStoreNative) Upsert(ctx context.Context, rec *SessionRecord) error {
	if s == nil || s.invoker == nil || rec == nil {
		return nil
	}
	started := time.Now()
	var opErr error
	defer func() { logDatlyStoreOp(ctx, "session", "upsert", strings.TrimSpace(rec.ID), started, opErr) }()
	userID := strings.TrimSpace(firstNonEmpty(rec.UserID, rec.Subject, rec.Email))
	if strings.TrimSpace(rec.ID) == "" || userID == "" {
		return nil
	}
	provider := strings.TrimSpace(rec.Provider)
	if provider == "" {
		provider = "local"
	}
	row := &write.Session{}
	row.SetId(strings.TrimSpace(rec.ID))
	row.SetUserId(userID)
	row.SetProvider(provider)
	if !rec.ExpiresAt.IsZero() {
		row.SetExpiresAt(rec.ExpiresAt)
	} else {
		row.SetExpiresAt(time.Now().Add(168 * time.Hour))
	}
	input := &write.Input{}
	input.SetSession([]*write.Session{row})
	_, opErr = s.invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: sessionWriterTarget, Input: input})
	return opErr
}

func (s *SessionStoreNative) Delete(ctx context.Context, id string) error {
	if s == nil || s.invoker == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	started := time.Now()
	var opErr error
	defer func() { logDatlyStoreOp(ctx, "session", "delete", strings.TrimSpace(id), started, opErr) }()
	row := &write.Session{}
	row.SetId(strings.TrimSpace(id))
	row.SetShouldDelete(true)
	input := &write.Input{}
	input.SetSession([]*write.Session{row})
	_, opErr = s.invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: sessionWriterTarget, Input: input})
	return opErr
}
