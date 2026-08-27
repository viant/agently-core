package data

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

const conversationDeleteDiagnosticsEnv = "AGENTLY_DEBUG_CONVERSATION_DELETE"

type conversationDeleteDiagnosticsContextKey struct{}

type conversationDeleteDiagnostics struct {
	id      string
	roots   []string
	started time.Time
}

var conversationDeleteDiagnosticsSequence atomic.Uint64

func conversationDeleteDiagnosticsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(conversationDeleteDiagnosticsEnv))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func beginConversationDeleteDiagnostics(ctx context.Context, rootIDs []string) (context.Context, *conversationDeleteDiagnostics) {
	if !conversationDeleteDiagnosticsEnabled() {
		return ctx, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	diagnostics := &conversationDeleteDiagnostics{
		id:      fmt.Sprintf("%x-%x", now.UnixNano(), conversationDeleteDiagnosticsSequence.Add(1)),
		roots:   append([]string(nil), rootIDs...),
		started: now,
	}
	ctx = context.WithValue(ctx, conversationDeleteDiagnosticsContextKey{}, diagnostics)
	diagnostics.logf("phase=request event=start roots=%q root_count=%d", summarizeConversationDeleteIDs(rootIDs), len(rootIDs))
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

func conversationDeleteDiagSQLStart(ctx context.Context, kind, statement string, idCount, chunk, chunks int) time.Time {
	diagnostics := conversationDeleteDiagnosticsFromContext(ctx)
	if diagnostics == nil {
		return time.Time{}
	}
	diagnostics.logf("phase=sql event=start kind=%s statement=%q ids=%d chunk=%d chunks=%d",
		kind, compactConversationDeleteStatement(statement), idCount, chunk, chunks)
	return time.Now()
}

func conversationDeleteDiagSQLDone(ctx context.Context, kind, statement string, idCount, chunk, chunks, rows int, affected int64, started time.Time, err error) {
	diagnostics := conversationDeleteDiagnosticsFromContext(ctx)
	if diagnostics == nil {
		return
	}
	details := fmt.Sprintf("kind=%s statement=%q ids=%d chunk=%d chunks=%d",
		kind, compactConversationDeleteStatement(statement), idCount, chunk, chunks)
	if kind == "exec" {
		details += fmt.Sprintf(" affected=%d", affected)
	} else {
		details += fmt.Sprintf(" rows=%d", rows)
	}
	diagnostics.logDone("sql", started, err, details)
}

func (d *conversationDeleteDiagnostics) logDone(phase string, started time.Time, err error, details string) {
	elapsed := time.Duration(0)
	if !started.IsZero() {
		elapsed = time.Since(started)
	}
	if err != nil {
		d.logf("phase=%s event=done elapsed_ms=%.3f error=%q %s", phase, elapsedMilliseconds(elapsed), err.Error(), strings.TrimSpace(details))
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

func conversationDeleteGraphDetails(graph *conversationDeleteGraph) string {
	if graph == nil {
		return ""
	}
	return fmt.Sprintf(
		"conversations=%d turns=%d messages=%d runs=%d approvals=%d schedule_runs=%d payloads=%d goals=%d schedules=%d report_runs=%d report_jobs=%d",
		len(graph.ConversationIDs),
		len(graph.TurnIDs),
		len(graph.MessageIDs),
		len(graph.RunIDs),
		len(graph.ApprovalIDs),
		len(graph.ScheduleRunIDs),
		len(graph.PayloadIDs),
		len(graph.GoalIDs),
		len(graph.ScheduleIDs),
		len(graph.ReportRunIDs),
		len(graph.ReportJobIDs),
	)
}
