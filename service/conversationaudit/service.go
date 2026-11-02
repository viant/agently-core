// Package conversationaudit exposes bounded, read-only views of saved turns.
package conversationaudit

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	convstore "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	projection "github.com/viant/agently-core/internal/datly/conversationaudit/read"
	"github.com/viant/agently-core/sdk"
)

var (
	ErrFilter    = errors.New("select a conversation or a complete date range of at most 31 days")
	ErrForbidden = errors.New("audit access denied")
	ErrNotFound  = errors.New("saved turn or payload not found")
	ErrTooLarge  = errors.New("selection exceeds audit limits; narrow the filters")
	ErrBusy      = errors.New("two audit exports are already running; try again later")
)

type Options struct {
	UIEnabled      bool
	AuditorUserIDs []string
}

type Filter struct {
	ConversationID string     `json:"conversationId,omitempty"`
	UserID         string     `json:"userId,omitempty"`
	From           *time.Time `json:"from,omitempty"`
	To             *time.Time `json:"to,omitempty"`
	Cursor         string     `json:"cursor,omitempty"`
	Limit          int        `json:"limit,omitempty"`
	IncludeContent bool       `json:"includeContent,omitempty"`
}

type Capabilities struct {
	UIEnabled        bool   `json:"uiEnabled"`
	CurrentUserID    string `json:"currentUserId"`
	CanAuditAllUsers bool   `json:"canAuditAllUsers"`
}

type Page struct {
	Rows       []*ListRow `json:"rows"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

// ListRow keeps the original lightweight projection and optionally adds the
// same saved content used by turn details and CSV, without exposing messages.
type ListRow struct {
	*projection.Row
	*Summary
}

type Summary struct {
	Input           string   `json:"input"`
	Output          string   `json:"output"`
	Models          []string `json:"models"`
	Warnings        []string `json:"warnings,omitempty"`
	ExecutionSpanMs *int64   `json:"executionSpanMs"`
}

type Detail struct {
	Row   *projection.Row `json:"row"`
	State *sdk.TurnState  `json:"state"`
	Summary
}

type Service struct {
	reader   Reader
	opts     Options
	auditors map[string]bool
	exports  chan struct{}
	payload  func(context.Context, string) (*convstore.Payload, error)
}

func New(reader Reader, opts Options, payload func(context.Context, string) (*convstore.Payload, error)) *Service {
	s := &Service{reader: reader, opts: opts, auditors: map[string]bool{}, exports: make(chan struct{}, 2), payload: payload}
	for _, id := range opts.AuditorUserIDs {
		if id = strings.TrimSpace(id); id != "" {
			s.auditors[id] = true
		}
	}
	return s
}

func (s *Service) Capabilities(ctx context.Context) (Capabilities, error) {
	id := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if id == "" {
		return Capabilities{}, ErrForbidden
	}
	return Capabilities{s.opts.UIEnabled, id, s.auditors[id]}, nil
}

type cursor struct {
	At    time.Time `json:"at"`
	ID    string    `json:"id"`
	Scope string    `json:"scope"`
}

func (s *Service) input(ctx context.Context, f Filter) (*projection.Input, string, error) {
	caps, err := s.Capabilities(ctx)
	if err != nil {
		return nil, "", err
	}
	f.ConversationID, f.UserID = strings.TrimSpace(f.ConversationID), strings.TrimSpace(f.UserID)
	if len(f.ConversationID) > 256 || len(f.UserID) > 256 || len(f.Cursor) > 2048 {
		return nil, "", ErrFilter
	}
	owner := caps.CurrentUserID
	if caps.CanAuditAllUsers {
		owner = f.UserID
	} else if f.UserID != "" && f.UserID != owner {
		return nil, "", ErrForbidden
	}
	if f.Limit == 0 {
		f.Limit = 50
	}
	if f.Limit < 1 || f.Limit > 200 {
		return nil, "", ErrFilter
	}
	i := &projection.Input{Allowed: true, OwnerID: owner, ConversationID: f.ConversationID, Limit: f.Limit + 1}
	if f.ConversationID == "" {
		if f.From == nil || f.To == nil || !f.To.After(*f.From) || f.To.Sub(*f.From) > 31*24*time.Hour {
			return nil, "", ErrFilter
		}
		i.From, i.To = f.From.UTC().Format(projection.DatabaseTimeFormat), f.To.UTC().Format(projection.DatabaseTimeFormat)
	}
	key, _ := json.Marshal([]any{owner, i.ConversationID, i.From, i.To})
	hash := sha256.Sum256(key)
	scope := hex.EncodeToString(hash[:])
	if f.Cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(f.Cursor)
		var c cursor
		if e != nil || json.Unmarshal(b, &c) != nil || c.Scope != scope || c.ID == "" || c.At.IsZero() {
			return nil, "", ErrFilter
		}
		i.CursorTime, i.CursorID = c.At, c.ID
	}
	return i, scope, nil
}

func (s *Service) List(ctx context.Context, f Filter) (*Page, error) {
	i, scope, err := s.input(ctx, f)
	if err != nil {
		return nil, err
	}
	rows, err := s.reader.Read(ctx, i)
	if err != nil {
		return nil, err
	}
	page := &Page{Rows: make([]*ListRow, 0, len(rows))}
	if len(rows) >= i.Limit {
		rows = rows[:i.Limit-1]
		last := rows[len(rows)-1]
		b, _ := json.Marshal(cursor{last.CreatedAt, last.Id, scope})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	if f.IncludeContent && len(rows) > 0 {
		rows, err = s.readPageContent(ctx, i, rows)
		if err != nil {
			return nil, err
		}
	}
	for _, row := range rows {
		item := &ListRow{Row: row}
		if f.IncludeContent {
			d, err := detailFromRow(row)
			if err != nil {
				return nil, err
			}
			item.Summary = &d.Summary
		}
		// Even enriched lists never return raw transcripts or run metadata.
		row.Message = nil
		row.Runs = nil
		page.Rows = append(page.Rows, item)
	}
	if f.IncludeContent {
		b, err := json.Marshal(page)
		if err != nil {
			return nil, err
		}
		if len(b)+1 > MaxExportBytes {
			return nil, ErrTooLarge
		}
	}
	return page, nil
}

func (s *Service) Detail(ctx context.Context, conversationID, turnID string) (*Detail, error) {
	if strings.TrimSpace(conversationID) == "" || strings.TrimSpace(turnID) == "" || len(turnID) > 256 {
		return nil, ErrFilter
	}
	i, _, err := s.input(ctx, Filter{ConversationID: conversationID, Limit: 1})
	if err != nil {
		return nil, err
	}
	i.TurnID, i.Detail = turnID, true
	rows, err := s.reader.Read(ctx, i)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return detailFromRow(rows[0])
}

// detailFromRow is audit-owned; the shared canonical builder stays unchanged.
func detailFromRow(row *projection.Row) (*Detail, error) {
	if len(row.Message) >= 10001 || len(row.Runs) >= 10001 {
		return nil, ErrTooLarge
	}
	b, err := json.Marshal(row)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxExportBytes {
		return nil, ErrTooLarge
	}
	var turn convread.TranscriptView
	if err = json.Unmarshal(b, &turn); err != nil {
		return nil, err
	}
	// Recreate the saved parent/child shape expected by the shared pure builder.
	parents := map[string]*convread.MessageView{}
	for _, m := range turn.Message {
		parents[m.Id] = m
	}
	for _, m := range turn.Message {
		if m.ParentMessageId == nil {
			continue
		}
		parent := parents[*m.ParentMessageId]
		if parent == nil || m.MessageToolCall == nil {
			continue
		}
		child := &convread.ToolMessageView{Id: m.Id, CreatedAt: m.CreatedAt, Type: m.Type, ParentMessageId: m.ParentMessageId, Sequence: m.Sequence, Content: m.Content, ToolName: m.ToolName, Iteration: m.Iteration, LinkedConversationId: m.LinkedConversationId}
		b, _ := json.Marshal(m.MessageToolCall)
		child.ToolCall = &convread.ToolCallView{}
		if err = json.Unmarshal(b, child.ToolCall); err != nil {
			return nil, err
		}
		parent.ToolMessage = append(parent.ToolMessage, child)
	}
	state := sdk.BuildCanonicalState(row.ConversationId, convstore.Transcript{(*convstore.Turn)(&turn)})
	if len(state.Turns) != 1 {
		return nil, fmt.Errorf("cannot reconstruct saved turn")
	}
	d := &Detail{Row: row, State: state.Turns[0]}
	d.Models = savedModels(row)
	d.ExecutionSpanMs = executionSpan(row.Runs)
	if row.ModelCalls > 0 && (row.InputTokens == nil || row.OutputTokens == nil || row.Cost == nil) {
		d.Warnings = append(d.Warnings, "Some model usage or cost measurements were not saved; incomplete totals are unknown.")
	}
	if d.State.User != nil {
		d.Input = d.State.User.Content
	}
	// Later user messages are steering within the same turn.
	for _, m := range d.State.Messages {
		if m.Role == "user" && (d.State.User == nil || m.MessageID != d.State.User.MessageID) {
			if d.Input != "" {
				d.Input += "\n\n"
			}
			d.Input += m.Content
		}
	}
	if d.State.Assistant != nil && d.State.Assistant.Final != nil {
		d.Output = d.State.Assistant.Final.Content
	}
	if d.Output == "" {
		d.Warnings = append(d.Warnings, "No saved final response. The turn may be incomplete.")
	}
	return d, nil
}

// ExecutionSpanMs covers the first saved run start through the last saved run
// completion, including waits between resumptions. Incomplete timing is unknown.
func executionSpan(runs []*projection.Run) *int64 {
	if len(runs) == 0 {
		return nil
	}
	var start, end time.Time
	for _, run := range runs {
		if run.StartedAt == nil || run.CompletedAt == nil {
			return nil
		}
		if start.IsZero() || run.StartedAt.Before(start) {
			start = *run.StartedAt
		}
		if end.IsZero() || run.CompletedAt.After(end) {
			end = *run.CompletedAt
		}
	}
	if end.Before(start) {
		return nil
	}
	span := end.Sub(start).Milliseconds()
	return &span
}
