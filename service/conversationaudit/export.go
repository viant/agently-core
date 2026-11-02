package conversationaudit

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strings"
	"time"
	"unicode"
)

const MaxExportRows = 1000
const MaxExportBytes = 32 << 20

// CSV buffers the complete result before HTTP headers are committed. A limit or
// timeout therefore returns an error, never a successful partial export.
func (s *Service) CSV(ctx context.Context, f Filter) ([]byte, error) {
	// Export hydrates each detail itself; do not also enrich its list pages.
	f.IncludeContent = false
	if _, _, err := s.input(ctx, f); err != nil {
		return nil, err
	}
	if f.Cursor != "" {
		return nil, ErrFilter
	}
	select {
	case s.exports <- struct{}{}:
		defer func() { <-s.exports }()
	default:
		return nil, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var buffer bytes.Buffer
	w := csv.NewWriter(&limitedWriter{buffer: &buffer, remaining: MaxExportBytes})
	// Keep the fields in the same order as the audit table. Execution details
	// remain available through Detail and are intentionally excluded from CSV.
	header := []string{"turn_created_at", "turn_id", "conversation_id", "conversation_title", "input", "output", "user_id", "agent_id", "status", "models", "input_tokens", "output_tokens", "execution_span_ms", "feedback_rating", "feedback_comment", "model_calls", "tool_calls", "conversation_created_at"}
	if err := w.Write(header); err != nil {
		return nil, err
	}
	f.Limit = 200
	count := 0
	for {
		page, err := s.List(ctx, f)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Rows {
			count++
			if count > MaxExportRows {
				return nil, ErrTooLarge
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			d, err := s.Detail(ctx, row.ConversationId, row.Id)
			if err != nil {
				return nil, err
			}
			// Detail rechecks current ownership. Keep the requested owner scope
			// even if the conversation changed while this export was running.
			if d.Row.OwnerID != row.OwnerID {
				return nil, ErrNotFound
			}
			r := d.Row
			fields := []string{r.CreatedAt.Format(time.RFC3339Nano), r.Id, r.ConversationId, textValue(r.ConversationTitle), d.Input, d.Output, r.OwnerID, textValue(r.AgentIdUsed), r.Status, strings.Join(d.Models, ", "), optional(r.InputTokens), optional(r.OutputTokens), optional(d.ExecutionSpanMs), optional(r.FeedbackRating), textValue(r.FeedbackComment), fmt.Sprint(r.ModelCalls), fmt.Sprint(r.ToolCalls), r.ConversationCreatedAt.Format(time.RFC3339Nano)}
			for i := range fields {
				// Typed numeric measurements and ratings must remain numeric,
				// including the valid negative feedback rating (-1).
				switch header[i] {
				case "input_tokens", "output_tokens", "execution_span_ms", "feedback_rating", "model_calls", "tool_calls":
					continue
				}
				fields[i] = csvCell(fields[i])
			}
			if err = w.Write(fields); err != nil {
				return nil, err
			}
		}
		if page.NextCursor == "" {
			break
		}
		f.Cursor = page.NextCursor
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func textValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func optional[T any](p *T) string {
	if p == nil {
		return ""
	}
	return fmt.Sprint(*p)
}
func csvCell(v string) string {
	trimmed := strings.TrimLeftFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\ufeff' })
	if strings.ContainsAny(v[:len(v)-len(trimmed)], "\t\r\n") || (len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
		return "'" + v
	}
	return v
}

type limitedWriter struct {
	buffer    *bytes.Buffer
	remaining int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, ErrTooLarge
	}
	w.remaining -= len(p)
	return w.buffer.Write(p)
}
