package data

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/viant/agently-core/internal/store/maintenancediag"
)

const conversationDeleteDiagnosticsEnv = maintenancediag.Env

type conversationDeleteDiagnosticsContextKey struct{}

type conversationDeleteDiagnostics struct {
	id      string
	roots   []string
	started time.Time
	trace   *maintenancediag.Trace
}

var conversationDeleteDiagnosticsSequence atomic.Uint64

func conversationDeleteDiagnosticsEnabled() bool {
	return maintenancediag.Enabled()
}

func beginConversationDeleteDiagnostics(ctx context.Context, rootIDs []string) (context.Context, *conversationDeleteDiagnostics) {
	if !conversationDeleteDiagnosticsEnabled() {
		return ctx, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now()
	ctx, trace := maintenancediag.Begin(ctx, "manual_conversation_delete")
	id := maintenancediag.ID(ctx)
	if id == "" {
		id = fmt.Sprintf("%x-%x", started.UnixNano(), conversationDeleteDiagnosticsSequence.Add(1))
	}
	diagnostics := &conversationDeleteDiagnostics{
		id:      id,
		roots:   append([]string(nil), rootIDs...),
		started: started,
		trace:   trace,
	}
	ctx = context.WithValue(ctx, conversationDeleteDiagnosticsContextKey{}, diagnostics)
	if trace == nil {
		diagnostics.logf("phase=request event=start roots=%q root_count=%d", summarizeConversationDeleteIDs(rootIDs), len(rootIDs))
	} else {
		diagnostics.logf("phase=roots roots=%q root_count=%d", summarizeConversationDeleteIDs(rootIDs), len(rootIDs))
	}
	return ctx, diagnostics
}

func conversationDeleteDiagnosticsFromContext(ctx context.Context) *conversationDeleteDiagnostics {
	if ctx == nil {
		return nil
	}
	diagnostics, _ := ctx.Value(conversationDeleteDiagnosticsContextKey{}).(*conversationDeleteDiagnostics)
	return diagnostics
}

func (d *conversationDeleteDiagnostics) finish(err error) {
	if d == nil {
		return
	}
	if d.trace != nil {
		d.trace.Finish(err)
		return
	}
	d.logDone("request", d.started, err, fmt.Sprintf("roots=%q root_count=%d", summarizeConversationDeleteIDs(d.roots), len(d.roots)))
}

func conversationDeleteDiagPhaseStart(ctx context.Context, phase string) time.Time {
	diagnostics := conversationDeleteDiagnosticsFromContext(ctx)
	if diagnostics == nil {
		return time.Time{}
	}
	diagnostics.logf("phase=%s event=start", phase)
	return time.Now()
}

func conversationDeleteDiagPhaseDone(ctx context.Context, phase string, started time.Time, err error, details string) {
	diagnostics := conversationDeleteDiagnosticsFromContext(ctx)
	if diagnostics == nil {
		return
	}
	diagnostics.logDone(phase, started, err, details)
}

func (d *conversationDeleteDiagnostics) logDone(phase string, started time.Time, err error, details string) {
	elapsed := time.Duration(0)
	if !started.IsZero() {
		elapsed = time.Since(started)
	}
	if err != nil {
		d.logf("phase=%s event=done elapsed_ms=%.3f error_type=%T %s", phase, elapsedMilliseconds(elapsed), err, strings.TrimSpace(details))
		return
	}
	d.logf("phase=%s event=done elapsed_ms=%.3f %s", phase, elapsedMilliseconds(elapsed), strings.TrimSpace(details))
}

func (d *conversationDeleteDiagnostics) logf(format string, args ...interface{}) {
	if d == nil {
		return
	}
	log.Printf("[conversation-delete] trace=%s "+format, append([]interface{}{d.id}, args...)...)
}

func elapsedMilliseconds(elapsed time.Duration) float64 {
	return float64(elapsed) / float64(time.Millisecond)
}

func compactConversationDeleteStatement(statement string) string {
	const maxLength = 240
	statement = strings.Join(strings.Fields(statement), " ")
	if len(statement) <= maxLength {
		return statement
	}
	return statement[:maxLength-3] + "..."
}

func summarizeConversationDeleteIDs(ids []string) string {
	const maxIDs = 10
	ids = normalizeDeleteIDs(ids)
	if len(ids) <= maxIDs {
		return strings.Join(ids, ",")
	}
	return fmt.Sprintf("%s,...(+%d)", strings.Join(ids[:maxIDs], ","), len(ids)-maxIDs)
}
