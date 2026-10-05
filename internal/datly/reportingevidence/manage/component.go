package manage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	store "github.com/viant/agently-core/app/store/reportingevidence"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	docread "github.com/viant/agently-core/internal/datly/reportingevidence/document/read"
	docwrite "github.com/viant/agently-core/internal/datly/reportingevidence/document/write"
	runread "github.com/viant/agently-core/internal/datly/reportingevidence/run/read"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	"github.com/viant/datly/runtime/handler/custom"
	"github.com/viant/datly/spec"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

type ManageComponent struct {
	Contract xdatly.Component[store.Request, store.Response] `component:"Manage,path=/v1/internal/agently/reporting-evidence/manage,method=POST,connector=agently,handler=NewManage,internal=true"`
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
func (*Manage) Exec(ctx context.Context, session handler.Session, input *store.Request, output *store.Response) error {
	if err := store.Validate(input); err != nil {
		return err
	}
	if session == nil || session.Binder() == nil || output == nil || authctx.EffectiveUserID(ctx) != input.OwnerID {
		return fmt.Errorf("evidence authenticated scope mismatch")
	}
	deps := struct {
		Invoker dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
		Starter handler.TransactionStarter `bind:"kind=transactionStarter,required"`
	}{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil || deps.Starter == nil {
		return fmt.Errorf("evidence transaction unavailable")
	}
	ctx = dexec.WithTransactionIsolation(ctx, dexec.IsolationSerializable)
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	call := func(pkg, name, method, route string, v any, lock bool) (any, error) {
		var options *dexec.ReaderOptions
		if method == "GET" {
			options = queryselectors.ForUpdateOptions(ctx, lock)
		}
		return deps.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: "github.com/viant/agently-core/internal/datly/reportingevidence/" + pkg, Name: name}, Route: spec.RouteRef{Method: method, Path: "/v1/internal/agently/reporting-evidence/" + route}}, Input: v, ReaderOptions: options})
	}
	query := &runread.Input{}
	query.SetOwner(input.OwnerID)
	query.SetConversationID(input.ConversationID)
	query.SetTurnID(input.TurnID)
	query.SetRunID(input.RunID)
	value, err := call("run/read", "reader", "GET", "run", query, input.Operation == "save")
	if err != nil {
		return err
	}
	runs, ok := value.(*runread.Output)
	if !ok || runs == nil || len(runs.Data) != 1 || runs.Data[0] == nil {
		return fmt.Errorf("evidence execution run not found")
	}
	run := runs.Data[0]
	if text(run.Id) != input.RunID || text(run.ConversationId) != input.ConversationID || text(run.TurnId) != input.TurnID || text(run.EffectiveUserId) != input.OwnerID {
		return fmt.Errorf("evidence execution identity mismatch")
	}
	if input.Operation == "save" && (text(run.Status) != "running" || text(run.LeaseOwner) != input.LeaseOwner || run.LeaseUntil == nil || !run.LeaseUntil.After(time.Now().UTC())) {
		return fmt.Errorf("evidence execution lease is not active")
	}
	id := store.ID(input.Scope, input.Subtype, input.PlanID)
	read := func() (*docread.Document, error) {
		q := &docread.Input{}
		q.SetID(id)
		q.SetRunID(input.RunID)
		q.SetSubtype(input.Subtype)
		q.SetOwner(input.OwnerID)
		q.SetConversationID(input.ConversationID)
		q.SetTurnID(input.TurnID)
		v, e := call("document/read", "reader", "GET", "document", q, false)
		if e != nil {
			return nil, e
		}
		o, ok := v.(*docread.Output)
		if !ok || o == nil || len(o.Data) > 1 {
			return nil, fmt.Errorf("evidence document read invalid")
		}
		if len(o.Data) == 0 {
			return nil, nil
		}
		d := o.Data[0]
		if d == nil || text(d.Id) != id || text(d.RunId) != input.RunID || text(d.Kind) != "attachment" || text(d.Subtype) != input.Subtype || text(d.SchemaRef) != store.Schema(input.Subtype) || text(d.MimeType) != "application/json" || text(d.Storage) != "inline" || text(d.Compression) != "none" || !json.Valid(d.InlineBody) || d.SizeBytes == nil || *d.SizeBytes != len(d.InlineBody) || text(d.Digest) != digest(d.InlineBody) {
			return nil, fmt.Errorf("evidence document integrity mismatch")
		}
		return d, nil
	}
	existing, err := read()
	if err != nil {
		return err
	}
	if input.Operation == "load" {
		if existing == nil {
			return store.ErrNotFound
		}
		output.Body = append(json.RawMessage(nil), existing.InlineBody...)
		return nil
	}
	var obj any
	decoder := json.NewDecoder(bytes.NewReader(input.Body))
	decoder.UseNumber()
	if err = decoder.Decode(&obj); err != nil {
		return err
	}
	body, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	if existing != nil {
		if !bytes.Equal(body, existing.InlineBody) {
			return fmt.Errorf("immutable evidence conflict")
		}
		output.Body = append(json.RawMessage(nil), body...)
		return nil
	}
	row := &docwrite.Document{}
	row.SetId(ptr(id))
	row.SetRunId(ptr(input.RunID))
	row.SetKind(ptr("attachment"))
	row.SetSubtype(ptr(input.Subtype))
	row.SetSchemaRef(ptr(store.Schema(input.Subtype)))
	row.SetMimeType(ptr("application/json"))
	row.SetStorage(ptr("inline"))
	row.SetCompression(ptr("none"))
	row.SetInlineBody(body)
	size := len(body)
	row.SetSizeBytes(&size)
	row.SetDigest(ptr(digest(body)))
	writer := &docwrite.Input{}
	writer.SetRows([]*docwrite.Document{row})
	if _, err = call("document/write", "writer", "POST", "document", writer, false); err != nil {
		return err
	}
	confirmed, err := read()
	if err != nil {
		return err
	}
	if confirmed == nil || !bytes.Equal(confirmed.InlineBody, body) {
		return fmt.Errorf("evidence document write unconfirmed")
	}
	output.Body = append(json.RawMessage(nil), body...)
	return nil
}
func text(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func ptr(v string) *string   { return &v }
func digest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
