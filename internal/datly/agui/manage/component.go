// Package manage owns the atomic AG-UI admission/continuation/journal workflow.
// Database reads and writes are exclusively transcribed Datly 1.0 components.
package manage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"reflect"
	"strings"

	store "github.com/viant/agently-core/app/store/agui"
	eventread "github.com/viant/agently-core/internal/datly/agui/event/read"
	pendingread "github.com/viant/agently-core/internal/datly/agui/pending/read"
	runread "github.com/viant/agently-core/internal/datly/agui/run/read"
	runwrite "github.com/viant/agently-core/internal/datly/agui/run/write"
	sourceread "github.com/viant/agently-core/internal/datly/agui/source/read"
	threadread "github.com/viant/agently-core/internal/datly/agui/thread/read"
	threadwrite "github.com/viant/agently-core/internal/datly/agui/thread/write"
	turnread "github.com/viant/agently-core/internal/datly/agui/turn/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	wire "github.com/viant/agently-core/protocol/agui"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	custom "github.com/viant/datly/runtime/handler/custom"
	"github.com/viant/datly/spec"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

type ManageComponent struct {
	Contract xdatly.Component[store.Request, store.Response] `component:"Manage,path=/v1/internal/agently/ag-ui/manage,method=POST,connector=agently,handler=NewManage,internal=true"`
}

var ManageDatly = reflect.TypeFor[ManageComponent]()

type Manage struct{}

func NewManage() handler.Contract[store.Request, store.Response] { return &Manage{} }
func (ManageComponent) DatlyHandler(name string) func() (rh.TypedHandler, error) {
	if name != "NewManage" {
		return nil
	}
	return custom.Factory(NewManage)
}

func (*Manage) Exec(ctx context.Context, session handler.Session, input *store.Request, output *store.Response) (err error) {
	if session == nil || session.Binder() == nil || input == nil || output == nil {
		return fmt.Errorf("AG-UI persistence invocation is incomplete")
	}
	if input.Principal == "" || input.ThreadID == "" {
		return store.ErrNotFound
	}
	deps := struct {
		Invoker dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
		Starter handler.TransactionStarter `bind:"kind=transactionStarter,required"`
	}{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil {
		return fmt.Errorf("AG-UI component capability is unavailable")
	}
	mutation := input.Operation == "admit" || input.Operation == "append" || input.Operation == "claim" || input.Operation == "renew" || input.Operation == "promote"
	snapshotRead := input.Operation == "replay" || input.Operation == "pending" || input.Operation == "active" || input.Operation == "turnruns" || input.Operation == "feedfacts"
	if mutation || snapshotRead {
		ctx = dexec.WithTransactionIsolation(ctx, dexec.IsolationSerializable)
		if deps.Starter == nil {
			return fmt.Errorf("AG-UI transaction capability is unavailable")
		}
		if err := deps.Starter.Start(ctx); err != nil {
			return err
		}
	}
	tx := &operation{ctx: ctx, invoker: deps.Invoker, principal: input.Principal, threadID: input.ThreadID, locked: mutation}
	defer func() {
		if err != nil {
			return
		}
		if output.Run != nil {
			err = tx.decorate(output.Run)
			if err != nil {
				return
			}
		}
		for _, run := range output.Runs {
			if err = tx.decorate(run); err != nil {
				return
			}
		}
	}()
	switch input.Operation {
	case "feedfacts":
		return tx.feedFacts(input, output)
	case "claim", "renew":
		return tx.claim(input, output)
	case "admit":
		return tx.admit(input, output)
	case "append":
		return tx.append(input, output)
	case "thread":
		thread, err := tx.thread()
		if err != nil {
			return err
		}
		if thread == nil {
			return store.ErrNotFound
		}
		output.Thread = projectThread(thread)
		return nil
	case "pending", "active":
		thread, err := tx.thread()
		if err != nil {
			return err
		}
		if thread == nil {
			return store.ErrNotFound
		}
		query := &pendingread.Input{}
		query.SetPrincipal(tx.principal)
		query.SetConversationID(str(thread.Id))
		query.SetActive(input.Operation == "active")
		value, err := tx.call("pending/read", "reader", "GET", query)
		if err != nil {
			return err
		}
		rows, ok := value.(*pendingread.Output)
		if !ok || rows == nil {
			return fmt.Errorf("AG-UI pending reader returned %T", value)
		}
		output.Runs = make([]*store.Run, 0, len(rows.Data))
		for _, row := range rows.Data {
			// The pending reader reuses the physical projection but owns its generated type.
			output.Runs = append(output.Runs, &store.Run{ConversationID: str(row.ConversationId), RecordID: str(row.Id), ThreadID: tx.threadID, RunID: str(row.RunId), Principal: str(row.Principal), ParentRunID: str(row.ParentRunId), PriorRunID: str(row.PriorRunId), ClientMessageID: str(row.ClientMessageId), TurnID: str(row.TurnId), InputHash: str(row.InputHash), Input: clone(row.InputJson), Status: str(row.Status), Revision: row.Revision, LastSequence: row.LastSequence, Pending: clone(row.PendingJson), ResumedByRunID: str(row.ResumedByRunId)})
		}
		return nil
	case "turnruns":
		if input.TurnID == "" {
			return store.ErrNotFound
		}
		thread, err := tx.thread()
		if err != nil {
			return err
		}
		if thread == nil {
			return store.ErrNotFound
		}
		query := &turnread.Input{}
		query.SetPrincipal(tx.principal)
		query.SetConversationID(str(thread.Id))
		query.SetTurnID(input.TurnID)
		value, err := tx.call("turn/read", "reader", "GET", query)
		if err != nil {
			return err
		}
		rows, ok := value.(*turnread.Output)
		if !ok || rows == nil {
			return fmt.Errorf("AG-UI turn reader returned %T", value)
		}
		output.Runs = make([]*store.Run, 0, len(rows.Data))
		for _, row := range rows.Data {
			if row == nil || str(row.Principal) != tx.principal || str(row.ConversationId) != tx.conversationID || str(row.TurnId) != input.TurnID {
				return fmt.Errorf("AG-UI turn run identity mismatch")
			}
			output.Runs = append(output.Runs, &store.Run{ConversationID: str(row.ConversationId), RecordID: str(row.Id), ThreadID: tx.threadID, RunID: str(row.RunId), Principal: str(row.Principal), ParentRunID: str(row.ParentRunId), PriorRunID: str(row.PriorRunId), ClientMessageID: str(row.ClientMessageId), TurnID: str(row.TurnId), InputHash: str(row.InputHash), Input: clone(row.InputJson), Status: str(row.Status), Revision: row.Revision, LastSequence: row.LastSequence, Pending: clone(row.PendingJson), ResumedByRunID: str(row.ResumedByRunId)})
		}
		return nil
	case "nativeThread":
		thread, err := tx.nativeThread(input.ThreadID)
		if err != nil {
			return err
		}
		if thread == nil || thread.Revision < 1 || str(thread.Principal) != tx.principal {
			return store.ErrNotFound
		}
		output.Thread = projectThread(thread)
		return nil
	case "promote":
		thread, err := tx.thread()
		if err != nil {
			return err
		}
		if thread == nil {
			return store.ErrNotFound
		}
		if thread.ProtocolOnly {
			row := &threadwrite.Thread{}
			row.SetId(thread.Id)
			row.SetPrincipal(ptr(tx.principal))
			row.SetRevision(thread.Revision)
			row.SetProtocolOnly(false)
			if err = tx.writeThread(row, "update", thread.Revision+1); err != nil {
				return err
			}
			thread.ProtocolOnly = false
			thread.Revision++
		}
		output.Thread = projectThread(thread)
		return nil
	case "get", "replay":
		if input.RunID == "" {
			return store.ErrNotFound
		}
		run, err := tx.run(input.RunID)
		if err != nil {
			return err
		}
		if run == nil {
			return store.ErrNotFound
		}
		output.Run = tx.projectRun(run)
		if input.Operation == "get" {
			return nil
		}
		if input.After < 0 || input.After > run.LastSequence || input.Limit < 0 {
			return store.ErrConflict
		}
		limit := input.Limit
		if limit == 0 {
			limit = 1000
		}
		if limit > 1000 {
			limit = 1000
		}
		query := &eventread.Input{}
		query.SetPrincipal(tx.principal)
		query.SetRunKey(str(run.Id))
		query.SetAfter(input.After)
		query.SetLimit(limit)
		result, err := tx.call("event/read", "reader", "GET", query)
		if err != nil {
			return err
		}
		rows, ok := result.(*eventread.Output)
		if !ok || rows == nil {
			return fmt.Errorf("AG-UI replay reader returned %T", result)
		}
		output.Events = make([]store.JournalEvent, 0, len(rows.Data))
		previous := input.After
		for _, row := range rows.Data {
			if row == nil || row.Sequence != previous+1 || str(row.RunId) != input.RunID || str(row.ThreadId) != tx.threadID {
				return fmt.Errorf("AG-UI journal sequence is inconsistent")
			}
			output.Events = append(output.Events, store.JournalEvent{Sequence: row.Sequence, Event: clone(row.EventJson)})
			previous = row.Sequence
		}
		return nil
	default:
		return store.ErrInvalidTransition
	}
}

type operation struct {
	ctx                 context.Context
	invoker             dexec.ComponentInvoker
	principal, threadID string
	conversationID      string
	locked              bool
}

func (t *operation) call(pkg, name, method string, input any) (any, error) {
	var readOptions *dexec.ReaderOptions
	if method == "GET" {
		readOptions = queryselectors.ForUpdateOptions(t.ctx, t.locked && (pkg == "thread/read" || pkg == "run/read" || pkg == "lease/read"))
	}
	result, err := t.invoker.InvokeComponent(t.ctx, dexec.ComponentRequest{
		Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: "github.com/viant/agently-core/internal/datly/agui/" + pkg, Name: name}, Route: spec.RouteRef{Method: method, Path: "/v1/internal/agently/ag-ui/" + pkg[:strings.LastIndex(pkg, "/")]}},
		Input:  input, ReaderOptions: readOptions,
	})
	if err != nil {
		return nil, fmt.Errorf("AG-UI %s: %w", pkg, err)
	}
	return result, nil
}
func (t *operation) thread() (*threadread.Thread, error) {
	input := &threadread.Input{}
	input.SetPrincipal(t.principal)
	input.SetKey(store.ThreadKey(t.threadID))
	value, err := t.call("thread/read", "reader", "GET", input)
	if err != nil {
		return nil, err
	}
	rows, ok := value.(*threadread.Output)
	if !ok || rows == nil {
		return nil, fmt.Errorf("AG-UI thread reader returned %T", value)
	}
	if len(rows.Data) > 1 {
		return nil, fmt.Errorf("AG-UI thread identity is ambiguous")
	}
	if len(rows.Data) == 0 {
		return nil, nil
	}
	row := rows.Data[0]
	if str(row.ThreadId) != t.threadID || str(row.Principal) != t.principal || row.Id == nil || *row.Id == "" {
		return nil, store.ErrConflict
	}
	t.conversationID = *row.Id
	return row, nil
}
func (t *operation) run(runID string) (*runread.Run, error) {
	input := &runread.Input{}
	input.SetPrincipal(t.principal)
	if t.conversationID == "" {
		thread, err := t.thread()
		if err != nil {
			return nil, err
		}
		if thread == nil {
			return nil, nil
		}
	}
	input.SetKey(store.RunKey(t.principal, t.conversationID, runID))
	value, err := t.call("run/read", "reader", "GET", input)
	if err != nil {
		return nil, err
	}
	rows, ok := value.(*runread.Output)
	if !ok || rows == nil {
		return nil, fmt.Errorf("AG-UI run reader returned %T", value)
	}
	if len(rows.Data) > 1 {
		return nil, fmt.Errorf("AG-UI run identity is ambiguous")
	}
	if len(rows.Data) == 0 {
		return nil, nil
	}
	row := rows.Data[0]
	if row.RunId != runID || str(row.ConversationId) != t.conversationID || str(row.Principal) != t.principal || str(row.RunKind) != "agui" {
		return nil, store.ErrConflict
	}
	return row, nil
}
func (t *operation) writeRun(row *runwrite.Run, mode string, desired int64) error {
	input := &runwrite.Input{}
	input.SetPrincipal(t.principal)
	input.SetMode(mode)
	input.SetRows([]*runwrite.Run{row})
	if mode == "update" {
		input.SetDesiredRevision(desired)
	}
	_, err := t.call("run/write", "writer", "PATCH", input)
	return err
}
func (t *operation) writeThread(row *threadwrite.Thread, mode string, desired int64) error {
	input := &threadwrite.Input{}
	input.SetPrincipal(t.principal)
	input.SetMode(mode)
	input.SetRows([]*threadwrite.Thread{row})
	if mode == "update" {
		input.SetDesiredRevision(desired)
	}
	_, err := t.call("thread/write", "writer", "PATCH", input)
	return err
}

func (t *operation) admit(input *store.Request, output *store.Response) error {
	a := input.Admission
	if a == nil || a.ThreadID != t.threadID || a.Principal != t.principal || a.RunID != input.RunID || a.RunID == "" {
		return store.ErrConflict
	}
	wireInput, err := wire.DecodeInput(a.Input)
	if err != nil {
		return err
	}
	if wireInput.ThreadID != a.ThreadID || wireInput.RunID != a.RunID || str(wireInput.ParentRunID) != a.ParentRunID {
		return store.ErrConflict
	}
	canonical, err := canonicalJSON(a.Input)
	if err != nil {
		return err
	}
	hash := a.InputHash
	if hash == "" {
		digest := sha256.Sum256(canonical)
		hash = hex.EncodeToString(digest[:])
	}
	thread, threadCreated, err := t.ensureThread()
	if err != nil {
		return err
	}
	existing, err := t.run(a.RunID)
	if err != nil {
		return err
	}
	if existing != nil {
		stored, err := canonicalJSON(existing.InputJson)
		if err != nil {
			return fmt.Errorf("stored AG-UI input is invalid: %w", err)
		}
		if str(existing.InputHash) != hash || !bytes.Equal(stored, canonical) || str(existing.ParentRunId) != a.ParentRunID || str(existing.PriorRunId) != a.PriorRunID || str(existing.ClientMessageId) != a.ClientMessageID {
			return store.ErrConflict
		}
		output.Run = t.projectRun(existing)
		return nil
	}

	if a.PriorRunID == "" && a.ClientMessageID != "" {
		query := &sourceread.Input{}
		query.SetPrincipal(t.principal)
		query.SetSourceKey(store.SourceKey(t.principal, t.conversationID, a.ClientMessageID))
		value, err := t.call("source/read", "reader", "GET", query)
		if err != nil {
			return err
		}
		rows, ok := value.(*sourceread.Output)
		if !ok || rows == nil {
			return fmt.Errorf("AG-UI source reader returned %T", value)
		}
		if len(rows.Data) > 0 {
			return store.ErrConflict
		}
	}
	if a.PriorRunID == "" && a.TurnID != "" {
		query := &turnread.Input{}
		query.SetPrincipal(t.principal)
		query.SetConversationID(t.conversationID)
		query.SetTurnID(a.TurnID)
		value, err := t.call("turn/read", "reader", "GET", query)
		if err != nil {
			return err
		}
		rows, ok := value.(*turnread.Output)
		if !ok || rows == nil {
			return fmt.Errorf("AG-UI turn reader returned %T", value)
		}
		if len(rows.Data) > 0 {
			return store.ErrConflict
		}
	}
	if !threadCreated {
		row := &threadwrite.Thread{}
		row.SetId(thread.Id)
		row.SetPrincipal(ptr(t.principal))
		row.SetRevision(thread.Revision)
		if err := t.writeThread(row, "update", thread.Revision+1); err != nil {
			return err
		}
	}

	turnID := a.TurnID
	if a.PriorRunID != "" {
		if a.PriorRunID == a.RunID || a.ExpectedPriorRevision < 1 {
			return store.ErrConflict
		}
		prior, err := t.run(a.PriorRunID)
		if err != nil {
			return err
		}
		if prior == nil {
			return store.ErrNotFound
		}
		if str(prior.Status) != store.StatusInterrupted || str(prior.ResumedByRunId) != "" || prior.Revision != a.ExpectedPriorRevision {
			return store.ErrConflict
		}
		if turnID != "" && turnID != str(prior.TurnId) {
			return store.ErrConflict
		}
		turnID = str(prior.TurnId)
		claim := &runwrite.Run{}
		claim.SetId(prior.Id)
		claim.SetRunKey(prior.RunKey)
		claim.SetPrincipal(ptr(t.principal))
		claim.SetRevision(prior.Revision)
		claim.SetResumedByRunId(a.RunID)
		if err := t.writeRun(claim, "update", prior.Revision+1); err != nil {
			return err
		}
	}
	row := &runwrite.Run{}
	row.SetId(ptr(uuid.NewString()))
	row.SetRunKind(ptr("agui"))
	row.SetConversationId(ptr(t.conversationID))
	row.SetRunKey(ptr(store.RunKey(t.principal, t.conversationID, a.RunID)))
	if a.PriorRunID == "" && a.ClientMessageID != "" {
		row.SetSourceKey(ptr(store.SourceKey(t.principal, t.conversationID, a.ClientMessageID)))
	}
	if a.PriorRunID == "" && turnID != "" {
		row.SetInitialTurnKey(ptr(store.InitialTurnKey(t.principal, t.conversationID, turnID)))
	}
	row.SetRunId(a.RunID)
	row.SetPrincipal(ptr(t.principal))
	row.SetParentRunId(a.ParentRunID)
	row.SetPriorRunId(a.PriorRunID)
	row.SetClientMessageId(a.ClientMessageID)
	row.SetTurnId(turnID)
	row.SetStatus(ptr(store.StatusAdmitted))
	row.SetInputHash(ptr(hash))
	row.SetInputJson(clone(a.Input))
	row.SetRevision(1)
	row.SetLastSequence(0)
	row.SetPendingJson([]byte(`[]`))
	row.SetResumedByRunId("")
	if err := t.writeRun(row, "create", 1); err != nil {
		return err
	}
	output.New = true
	output.Run = &store.Run{ConversationID: t.conversationID, RecordID: str(row.Id), ThreadID: t.threadID, RunID: a.RunID, Principal: t.principal, ParentRunID: a.ParentRunID, PriorRunID: a.PriorRunID, ClientMessageID: a.ClientMessageID, TurnID: turnID, InputHash: hash, Input: clone(a.Input), Status: store.StatusAdmitted, Revision: 1, Pending: json.RawMessage(`[]`)}
	return nil
}

func ptr(value string) *string { return &value }
func str(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case *string:
		if v != nil {
			return *v
		}
	}
	return ""
}

func clone(value []byte) json.RawMessage { return append(json.RawMessage(nil), value...) }
func canonicalJSON(raw []byte) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func (t *operation) projectRun(row *runread.Run) *store.Run {
	return &store.Run{ConversationID: str(row.ConversationId), RecordID: str(row.Id), ThreadID: t.threadID, RunID: str(row.RunId), Principal: str(row.Principal), ParentRunID: str(row.ParentRunId), PriorRunID: str(row.PriorRunId), ClientMessageID: str(row.ClientMessageId), TurnID: str(row.TurnId), InputHash: str(row.InputHash), Input: clone(row.InputJson), Status: str(row.Status), Revision: row.Revision, LastSequence: row.LastSequence, Pending: clone(row.PendingJson), ResumedByRunID: str(row.ResumedByRunId)}
}
func projectThread(row *threadread.Thread) *store.Thread {
	return &store.Thread{ConversationID: str(row.Id), ProtocolOnly: row.ProtocolOnly, ThreadID: str(row.ThreadId), Principal: str(row.Principal), Revision: row.Revision, State: clone(row.StateJson), Messages: clone(row.MessagesJson)}
}
