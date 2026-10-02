package toolexecutionclaim

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"
	read "github.com/viant/agently-core/internal/datly/toolexecutionclaim/read"
	write "github.com/viant/agently-core/internal/datly/toolexecutionclaim/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/sqlx/io/errx"
)

// Store owns the claim protocol while generated components own every read and
// write. The claim key's database uniqueness resolves concurrent first claims.
type Store struct{ Invoker dexec.ComponentInvoker }

var readerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-execution-claim"},
}
var writerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/tool-execution-claim"},
}

func access(mode string) []locator.Provider {
	return []locator.Provider{provider.Named("claimaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "internal":
			return true, true, nil
		case "mode":
			return mode, true, nil
		}
		return nil, false, nil
	})}
}

func (s *Store) exists(ctx context.Context, key string) (bool, error) {
	if s == nil || s.Invoker == nil {
		return false, fmt.Errorf("claim component store is not configured")
	}
	query := &read.Input{}
	query.SetClaimKey(key)
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: query, Providers: access("")})
	if err != nil {
		return false, err
	}
	out, ok := value.(*read.Output)
	if !ok || out == nil {
		return false, fmt.Errorf("claim reader returned %T", value)
	}
	return len(out.Data) != 0, nil
}

func (s *Store) write(ctx context.Context, mode string, claim *write.Claim) error {
	if s == nil || s.Invoker == nil {
		return fmt.Errorf("claim component store is not configured")
	}
	input := &write.Input{}
	input.SetClaims([]*write.Claim{claim})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: writerTarget, Input: input, Providers: access(mode)})
	if err != nil {
		return err
	}
	if _, ok := value.(*write.Output); !ok {
		return fmt.Errorf("claim writer returned %T", value)
	}
	return nil
}

func (s *Store) Claim(ctx context.Context, row *write.Claim) (bool, error) {
	if row == nil || strings.TrimSpace(row.ClaimKey) == "" {
		return false, fmt.Errorf("claim key is required")
	}
	for attempt := 0; ; attempt++ {
		claimed, err := s.claimOnce(ctx, row)
		if err == nil || !isSQLiteLock(err) || attempt >= 11 {
			return claimed, err
		}
		delay := time.Duration(attempt+1) * 5 * time.Millisecond
		if delay > 25*time.Millisecond {
			delay = 25 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Store) claimOnce(ctx context.Context, row *write.Claim) (bool, error) {
	if err := s.write(ctx, "claim", row); err != nil {
		if errors.Is(err, write.ErrAlreadyClaimed) {
			return false, nil
		}
		if isUniqueViolation(err) {
			exists, lookupErr := s.exists(ctx, row.ClaimKey)
			if lookupErr != nil {
				return false, fmt.Errorf("verify claim key after duplicate write: %w", lookupErr)
			}
			if exists {
				return false, nil
			}
		}
		return false, err
	}
	return true, nil
}

func (s *Store) Finish(ctx context.Context, key, state string, finishedAt time.Time) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	row := &write.Claim{}
	row.SetClaimKey(key)
	row.SetState(state)
	row.SetUpdatedAt(&finishedAt)
	row.SetFinishedAt(&finishedAt)
	err := s.write(ctx, "finish", row)
	if errors.Is(err, write.ErrClaimMissing) {
		return nil
	}
	return err
}

func isUniqueViolation(err error) bool { return errx.IsDuplicateKey(err) }

func isSQLiteLock(err error) bool {
	var sqliteErr sqlite3.Error
	return errors.As(err, &sqliteErr) && (sqliteErr.Code == sqlite3.ErrBusy || sqliteErr.Code == sqlite3.ErrLocked)
}
