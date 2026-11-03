package conversationaudit

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
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
	return s.export(ctx, f, false)
}

// JSON exports only the table fields, with original text and typed values.
func (s *Service) JSON(ctx context.Context, f Filter) ([]byte, error) {
	return s.export(ctx, f, true)
}

// export shares authorization, selection, mapping and limits across formats.
// Both formats are buffered completely before the caller can send a file.
func (s *Service) export(ctx context.Context, f Filter, asJSON bool) ([]byte, error) {
	// Export hydrates each detail itself; do not also enrich its list pages.
	f.IncludeContent = false
	f.IncludeTotal = false
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
	bounded := &limitedWriter{buffer: &buffer, remaining: MaxExportBytes}
	w := csv.NewWriter(bounded)
	if asJSON {
		_, _ = bounded.Write([]byte("["))
	} else {
		if err := w.Write(exportHeader); err != nil {
			return nil, err
		}
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
			record := newExportRecord(d)
			if asJSON {
				if count > 1 {
					if _, err = bounded.Write([]byte(",")); err != nil {
						return nil, err
					}
				}
				if err = json.NewEncoder(bounded).Encode(record); err != nil {
					return nil, err
				}
			} else if err = w.Write(record.csvFields()); err != nil {
				return nil, err
			}
		}
		if page.NextCursor == "" {
			break
		}
		f.Cursor = page.NextCursor
	}
	if asJSON {
		if _, err := bounded.Write([]byte("]")); err != nil {
			return nil, err
		}
	} else {
		w.Flush()
		if err := w.Error(); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

var exportHeader = []string{"turn_created_at", "turn_id", "conversation_id", "conversation_title", "input", "output", "user_id", "agent_id", "status", "models", "input_tokens", "output_tokens", "execution_span_ms", "feedback_rating", "feedback_comment", "model_calls", "tool_calls", "conversation_created_at"}

// Field order is shared with the table and CSV. Unknown measurements stay null
// in JSON and empty in CSV; the CSV formula guard never modifies JSON text.
type exportRecord struct {
	TurnCreatedAt         string   `json:"turn_created_at"`
	TurnID                string   `json:"turn_id"`
	ConversationID        string   `json:"conversation_id"`
	ConversationTitle     string   `json:"conversation_title"`
	Input                 string   `json:"input"`
	Output                string   `json:"output"`
	UserID                string   `json:"user_id"`
	AgentID               string   `json:"agent_id"`
	Status                string   `json:"status"`
	Models                []string `json:"models"`
	InputTokens           *int     `json:"input_tokens"`
	OutputTokens          *int     `json:"output_tokens"`
	ExecutionSpanMs       *int64   `json:"execution_span_ms"`
	FeedbackRating        *int     `json:"feedback_rating"`
	FeedbackComment       string   `json:"feedback_comment"`
	ModelCalls            int      `json:"model_calls"`
	ToolCalls             int      `json:"tool_calls"`
	ConversationCreatedAt string   `json:"conversation_created_at"`
}

func newExportRecord(d *Detail) exportRecord {
	r := d.Row
	models := append([]string{}, d.Models...)
	return exportRecord{r.CreatedAt.Format(time.RFC3339Nano), r.Id, r.ConversationId,
		textValue(r.ConversationTitle), d.Input, d.Output, r.OwnerID, textValue(r.AgentIdUsed), r.Status, models,
		r.InputTokens, r.OutputTokens, d.ExecutionSpanMs, r.FeedbackRating, textValue(r.FeedbackComment),
		r.ModelCalls, r.ToolCalls, r.ConversationCreatedAt.Format(time.RFC3339Nano)}
}

func (r exportRecord) csvFields() []string {
	fields := []string{r.TurnCreatedAt, r.TurnID, r.ConversationID, r.ConversationTitle, r.Input, r.Output,
		r.UserID, r.AgentID, r.Status, strings.Join(r.Models, ", "), optional(r.InputTokens), optional(r.OutputTokens),
		optional(r.ExecutionSpanMs), optional(r.FeedbackRating), r.FeedbackComment,
		fmt.Sprint(r.ModelCalls), fmt.Sprint(r.ToolCalls), r.ConversationCreatedAt}
	for i := range fields {
		switch exportHeader[i] {
		case "input_tokens", "output_tokens", "execution_span_ms", "feedback_rating", "model_calls", "tool_calls":
			continue
		}
		fields[i] = csvCell(fields[i])
	}
	return fields
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
