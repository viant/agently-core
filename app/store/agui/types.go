// Package agui stores durable protocol identity, continuation, shared projection,
// and replay records through the application's native Datly component runtime.
package agui

import (
	"context"
	"encoding/json"
	"errors"
	wire "github.com/viant/agently-core/protocol/agui"
	"time"
)

var (
	ErrNotFound          = errors.New("AG-UI run or thread not found")
	ErrConflict          = errors.New("AG-UI identity or revision conflict")
	ErrInvalidTransition = errors.New("AG-UI invalid lifecycle transition")
)

const (
	StatusAdmitted    = "admitted"
	StatusRunning     = "running"
	StatusInterrupted = "interrupted"
	StatusFinished    = "finished"
	StatusError       = "error"
	StatusCancelled   = "cancelled"
)

type Run struct {
	ConversationID  string          `json:"-"`
	RecordID        string          `json:"-"`
	ClientMessageID string          `json:"clientMessageId,omitempty"`
	PriorRunID      string          `json:"priorRunId,omitempty"`
	ThreadID        string          `json:"threadId"`
	RunID           string          `json:"runId"`
	Principal       string          `json:"-"`
	ParentRunID     string          `json:"parentRunId,omitempty"`
	TurnID          string          `json:"turnId,omitempty"`
	InputHash       string          `json:"inputHash"`
	Input           json.RawMessage `json:"input"`
	Status          string          `json:"status"`
	Revision        int64           `json:"revision"`
	LastSequence    int64           `json:"lastSequence"`
	Pending         json.RawMessage `json:"pending,omitempty"`
	LeaseOwner      string          `json:"leaseOwner,omitempty"`
	LeaseUntil      *time.Time      `json:"leaseUntil,omitempty"`
	LeaseRevision   int64           `json:"leaseRevision,omitempty"`
	ResumedByRunID  string          `json:"resumedByRunId,omitempty"`
}

type Thread struct {
	ProtocolOnly   bool            `json:"-"`
	ConversationID string          `json:"-"`
	ThreadID       string          `json:"threadId"`
	Principal      string          `json:"-"`
	Revision       int64           `json:"revision"`
	State          json.RawMessage `json:"state"`
	Messages       json.RawMessage `json:"messages"`
}

type Admission struct {
	ClientMessageID       string
	ThreadID              string
	RunID                 string
	Principal             string
	ParentRunID           string
	TurnID                string
	InputHash             string
	Input                 json.RawMessage
	PriorRunID            string
	ExpectedPriorRevision int64
}

// Change combines the journal append and projection/lifecycle updates in one
// transaction. Nil fields mean unchanged; RawMessage("null") persists JSON null.
// ExpectedThreadRevision is required whenever State or Messages changes.
type Change struct {
	LeaseOwner             string
	Status                 string
	TurnID                 string
	Pending                json.RawMessage
	State                  json.RawMessage
	Messages               json.RawMessage
	ExpectedThreadRevision int64
}

type JournalEvent struct {
	Sequence int64           `json:"sequence"`
	Event    json.RawMessage `json:"event"`
}

type Store interface {
	Admit(context.Context, Admission) (*Run, bool, error)
	GetRun(ctx context.Context, principal, threadID, runID string) (*Run, error)
	Append(ctx context.Context, principal, threadID, runID string, expectedRevision int64, events []json.RawMessage, change *Change) (*Run, error)
	Replay(ctx context.Context, principal, threadID, runID string, after int64, limit int) ([]JournalEvent, error)
	GetThread(ctx context.Context, principal, threadID string) (*Thread, error)
	ListPending(ctx context.Context, principal, threadID string) ([]*Run, error)
	Claim(ctx context.Context, principal, threadID, runID string, expectedRunRevision int64, owner string, ttl time.Duration) (*Run, error)
	Renew(ctx context.Context, principal, threadID, runID string, expectedLeaseRevision int64, owner string, ttl time.Duration) (*Run, error)
}

// ActiveRunLister is an optional read capability. ListPending retains its
// interrupted-continuation semantics; active discovery never changes it.
type ActiveRunLister interface {
	ListActive(ctx context.Context, principal, threadID string) ([]*Run, error)
}

// TurnRunLister reads original durable ownership, including interrupted parents
// already consumed by a continuation. It does not alter ListPending semantics.
type TurnRunLister interface {
	ListRunsByTurn(context.Context, string, string, string) ([]*Run, error)
}

// Request/Response are the internal typed orchestration contract, never public
// HTTP inputs. Caller code maps its authenticated principal into Principal.
type Request struct {
	FeedID           string            `parameter:"FeedID,kind=body,in=feedId"`
	TurnID           string            `parameter:"TurnID,kind=body,in=turnId"`
	LeaseOwner       string            `parameter:"LeaseOwner,kind=body,in=leaseOwner"`
	TTL              time.Duration     `parameter:"TTL,kind=body,in=ttl"`
	Operation        string            `parameter:"Operation,kind=body,in=operation"`
	Principal        string            `parameter:"Principal,kind=body,in=principal"`
	ThreadID         string            `parameter:"ThreadID,kind=body,in=threadId"`
	RunID            string            `parameter:"RunID,kind=body,in=runId"`
	Admission        *Admission        `parameter:"Admission,kind=body,in=admission"`
	ExpectedRevision int64             `parameter:"ExpectedRevision,kind=body,in=expectedRevision"`
	Events           []json.RawMessage `parameter:"Events,kind=body,in=events"`
	Change           *Change           `parameter:"Change,kind=body,in=change"`
	After            int64             `parameter:"After,kind=body,in=after"`
	Limit            int               `parameter:"Limit,kind=body,in=limit"`
}
type Response struct {
	Run       *Run
	Thread    *Thread
	New       bool
	Events    []JournalEvent
	Runs      []*Run
	FeedFacts []wire.FeedLifecycleFact
}

// ConversationThreadResolver maps an already authorized native conversation to
// its exact wire identity; it never guesses by trimming or case folding.
type ConversationThreadResolver interface {
	GetThreadByConversationID(context.Context, string, string) (*Thread, error)
}
type ThreadPromoter interface {
	PromoteThread(context.Context, string, string) (*Thread, error)
}
