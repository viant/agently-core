package data

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/store/maintenancediag"
)

const conversationDeleteDiagnosticsEnv = maintenancediag.Env

type conversationDeleteDiagnosticsContextKey struct{}

type conversationDeleteDiagnostics struct {
	id    string
	trace *maintenancediag.Trace
}

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
	ctx, trace := maintenancediag.Begin(ctx, "manual_conversation_delete")
	diagnostics := &conversationDeleteDiagnostics{
		id:    maintenancediag.ID(ctx),
		trace: trace,
	}
	ctx = context.WithValue(ctx, conversationDeleteDiagnosticsContextKey{}, diagnostics)
	diagnostics.logf("phase=roots roots=%q root_count=%d", summarizeConversationDeleteIDs(rootIDs), len(rootIDs))
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
	d.trace.Finish(err)
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
