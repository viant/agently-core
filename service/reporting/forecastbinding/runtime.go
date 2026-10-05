package forecastbinding

import (
	"context"
	"encoding/json"
	"sort"
)

type PlanStore interface {
	SourceStore
	LoadPlan(context.Context, Scope, string) (*Plan, error)
	SavePlan(context.Context, *Plan, string) error
}

// Runtime is explicitly injected by the host; constructing it does not install
// a writer gate or change unrelated tool results.
type Runtime struct {
	producer PolicyProducer
	store    PlanStore
}

func NewRuntime(producer PolicyProducer, store PlanStore) (*Runtime, error) {
	if producer == nil || store == nil {
		return nil, reject("evidence dependencies unavailable")
	}
	return &Runtime{producer: producer, store: store}, nil
}
func (r *Runtime) AdmitPlan(ctx context.Context, scope Scope, profileOp, conversionOp, lease string) (*Plan, error) {
	a, e := r.store.LoadAdmission(ctx, scope)
	if e != nil {
		return nil, e
	}
	if a == nil || a.Scope != scope {
		return nil, reject("admission scope mismatch")
	}
	p, e := r.store.LoadCompletedCall(ctx, scope, profileOp)
	if e != nil {
		return nil, e
	}
	c, e := r.store.LoadCompletedCall(ctx, scope, conversionOp)
	if e != nil {
		return nil, e
	}
	if p == nil || c == nil {
		return nil, reject("plan source missing")
	}
	plan, e := NewPlan(ctx, r.producer, *a, PlanSources{Profile: *p, Conversion: *c})
	if e != nil {
		return nil, e
	}
	if e = r.store.SavePlan(ctx, plan, lease); e != nil {
		return nil, e
	}
	return r.store.LoadPlan(ctx, scope, plan.ID)
}

type Receipt struct {
	Profile         string          `json:"profile"`
	PlanID          string          `json:"planId"`
	Date            string          `json:"date"`
	Columns         []string        `json:"columns"`
	OpID            string          `json:"opId"`
	RequestHash     string          `json:"requestHash"`
	ResponseHash    string          `json:"responseHash"`
	ResponsePointer string          `json:"responsePointer"`
	SelectionOrigin SelectionOrigin `json:"selectionOrigin"`
	DateOrigin      DateOrigin      `json:"dateOrigin"`
}

// DecorateCompleted never trusts a caller's provisional result. It rereads the
// persisted completed call under the authenticated scope before releasing a
// receipt. Overflow/incomplete results cannot acquire a successful receipt.
func (r *Runtime) DecorateCompleted(ctx context.Context, scope Scope, planID, opID string) (json.RawMessage, error) {
	p, e := r.store.LoadPlan(ctx, scope, planID)
	if e != nil {
		return nil, e
	}
	if p == nil || p.Admission.Scope != scope {
		return nil, reject("plan scope mismatch")
	}
	if e = p.ValidateIdentity(); e != nil {
		return nil, e
	}
	c, e := r.store.LoadCompletedCall(ctx, scope, opID)
	if e != nil {
		return nil, e
	}
	if c == nil || c.Scope != scope || c.Status != "completed" {
		return nil, reject("source completion unconfirmed")
	}
	response, e := object(c.Response)
	if e != nil {
		return nil, e
	}
	for _, key := range []string{ReceiptKey, PlanReceiptKey, "_agentlyForecastProfile"} {
		if _, collision := response[key]; collision {
			return nil, reject("reserved evidence key collision")
		}
	}
	request, e := object(c.Request)
	if e != nil {
		return nil, e
	}
	filters, ok := request["filters"].(map[string]any)
	if !ok {
		return nil, reject("missing source filters")
	}
	timestamp, ok := filters["date"].(string)
	if !ok || len(timestamp) < 10 {
		return nil, reject("missing source date")
	}
	day := timestamp[:10]
	hash, e := RequestHash(c.Request)
	if e != nil {
		return nil, e
	}
	ref := Reference{OpID: c.OpID, RequestHash: hash, ResponsePointer: AvailsPointer}
	allowedDay := false
	for _, date := range p.Produced.Policy.Dates {
		if date == day {
			allowedDay = true
		}
	}
	if !allowedDay {
		return nil, reject("source date outside plan")
	}
	var columns []string
	for column, template := range p.Produced.Policy.Templates {
		_, e := Materialize(scope, Policy{Dates: []string{day}, Templates: map[string]json.RawMessage{column: template}}, Bindings{Profile: Profile, Columns: []Column{{Key: column, Calls: []Reference{ref}}}}, []Call{*c})
		if e == nil {
			columns = append(columns, column)
		}
	}
	if len(columns) == 0 {
		return nil, reject("completed source does not satisfy plan")
	}
	sort.Strings(columns)
	responseHash, e := RequestHash(c.Response)
	if e != nil {
		return nil, e
	}
	response[ReceiptKey] = Receipt{Profile: Profile, PlanID: planID, Date: day, Columns: columns, OpID: opID, RequestHash: hash, ResponseHash: responseHash, ResponsePointer: AvailsPointer, SelectionOrigin: p.Admission.SelectionOrigin, DateOrigin: p.Admission.DateOrigin}
	return json.Marshal(response)
}

type ProjectionStore interface {
	PublishCompletedProjection(context.Context, Scope, string, json.RawMessage) error
}

func (r *Runtime) DecorateAndPublishCompleted(ctx context.Context, scope Scope, planID, opID string) (json.RawMessage, error) {
	body, e := r.DecorateCompleted(ctx, scope, planID, opID)
	if e != nil {
		return nil, e
	}
	writer, ok := r.store.(ProjectionStore)
	if !ok {
		return nil, reject("durable receipt projection unavailable")
	}
	if e = writer.PublishCompletedProjection(ctx, scope, opID, body); e != nil {
		return nil, e
	}
	return body, nil
}
