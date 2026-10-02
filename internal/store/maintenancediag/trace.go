// Package maintenancediag provides opt-in timings for database maintenance.
// It never logs component inputs, SQL parameters, message bodies or credentials.
package maintenancediag

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	dexec "github.com/viant/datly/exec"
)

const Env = "AGENTLY_DEBUG_CONVERSATION_DELETE"

type contextKey struct{}
type Trace struct {
	id        string
	operation string
	started   time.Time
	calls     atomic.Uint64
}

var sequence atomic.Uint64

func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(Env))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func from(ctx context.Context) *Trace {
	if ctx == nil {
		return nil
	}
	trace, _ := ctx.Value(contextKey{}).(*Trace)
	return trace
}

// ID allows legacy facade diagnostics to share the managed operation's trace.
func ID(ctx context.Context) string {
	if trace := from(ctx); trace != nil {
		return trace.id
	}
	return ""
}

// Begin returns no owner for a nested operation. Only the outer owner finishes
// the trace, after the managed invocation has committed or rolled back.
func Begin(ctx context.Context, operation string) (context.Context, *Trace) {
	if from(ctx) != nil || !Enabled() {
		return ctx, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	trace := &Trace{id: fmt.Sprintf("%x-%x", now.UnixNano(), sequence.Add(1)), operation: operation, started: now}
	ctx = context.WithValue(ctx, contextKey{}, trace)
	trace.log("phase=request event=start operation=%s", operation)
	return ctx, trace
}

func (t *Trace) Finish(err error) {
	if t == nil {
		return
	}
	t.log("phase=request event=done operation=%s elapsed_ms=%.3f component_calls=%d success=%t error_type=%T", t.operation, ms(time.Since(t.started)), t.calls.Load(), err == nil, err)
}

// Phase is a no-op with diagnostics disabled. Details must contain only
// identities/counts, never data from the rows being deleted.
func Phase(ctx context.Context, name string) func(error, string) {
	t := from(ctx)
	if t == nil {
		return func(error, string) {}
	}
	started, calls := time.Now(), t.calls.Load()
	t.log("phase=%s event=start", name)
	return func(err error, details string) {
		t.log("phase=%s event=done elapsed_ms=%.3f component_calls=%d success=%t error_type=%T %s", name, ms(time.Since(started)), t.calls.Load()-calls, err == nil, err, details)
	}
}

// Wrap is applied only after capability binding. It must not obscure schema,
// driver, transaction starter or mutation reporter capabilities on the runtime.
func Wrap(ctx context.Context, invoker dexec.ComponentInvoker) dexec.ComponentInvoker {
	if from(ctx) == nil || invoker == nil {
		return invoker
	}
	if _, ok := invoker.(*timedInvoker); ok {
		return invoker
	}
	return &timedInvoker{invoker: invoker}
}

type timedInvoker struct{ invoker dexec.ComponentInvoker }

func (i *timedInvoker) InvokeComponent(ctx context.Context, request dexec.ComponentRequest) (any, error) {
	t := from(ctx)
	if t == nil {
		return i.invoker.InvokeComponent(ctx, request)
	}
	t.calls.Add(1)
	started := time.Now()
	value, err := i.invoker.InvokeComponent(ctx, request)
	// A component invocation can execute several SQL statements. This counter
	// deliberately does NOT claim to count queries or affected rows.
	t.log("phase=component event=done component=%s/%s elapsed_ms=%.3f success=%t error_type=%T", request.Target.Component.Scope, request.Target.Component.Name, ms(time.Since(started)), err == nil, err)
	return value, err
}

func ms(duration time.Duration) float64 { return float64(duration) / float64(time.Millisecond) }
func (t *Trace) log(format string, args ...any) {
	log.Printf("[conversation-delete] trace=%s "+format, append([]any{t.id}, args...)...)
}
