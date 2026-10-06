package forecastbinding

import (
	"bytes"
	"context"
	"encoding/json"
)

// DataBindings is consumed on the server before an envelope reaches strict
// Forge/SDK readers. A plan identifier is a reference, never authorization.
type DataBindings struct {
	PlanID  string   `json:"planId"`
	Profile string   `json:"profile"`
	Columns []Column `json:"columns"`
}

// BindDataEnvelope verifies or materializes a newly authored forecast data
// envelope from immutable source records. It is never applied to historical
// reads. Supplied rows must match exactly; absent rows are deterministically
// materialized. The model-only binding field is removed before publication.
func (r *Runtime) BindDataEnvelope(ctx context.Context, scope Scope, raw json.RawMessage) (json.RawMessage, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope == nil {
		return nil, reject("invalid forecast data envelope")
	}
	bindingRaw, ok := envelope["sourceBindings"]
	if !ok {
		return nil, reject("forecast data requires sourceBindings")
	}
	var binding DataBindings
	decoder := json.NewDecoder(bytes.NewReader(bindingRaw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&binding); err != nil || binding.PlanID == "" {
		return nil, reject("invalid forecast sourceBindings")
	}
	plan, err := r.store.LoadPlan(ctx, scope, binding.PlanID)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.Admission.Scope != scope || plan.ID != binding.PlanID {
		return nil, reject("publication plan scope mismatch")
	}
	if err = plan.ValidateIdentity(); err != nil {
		return nil, err
	}
	var records []Call
	loaded := map[string]bool{}
	for _, column := range binding.Columns {
		for _, reference := range column.Calls {
			if loaded[reference.OpID] {
				continue
			}
			call, err := r.store.LoadCompletedCall(ctx, scope, reference.OpID)
			if err != nil {
				return nil, err
			}
			if call == nil {
				return nil, reject("publication source missing")
			}
			records = append(records, *call)
			loaded[reference.OpID] = true
		}
	}
	bindings := Bindings{Profile: binding.Profile, Columns: binding.Columns}
	var result *Result
	if data, present := envelope["data"]; present {
		result, err = Validate(scope, plan.Produced.Policy, bindings, records, data)
	} else {
		result, err = Materialize(scope, plan.Produced.Policy, bindings, records)
	}
	if err != nil {
		return nil, err
	}
	envelope["data"] = result.Data
	delete(envelope, "sourceBindings")
	return json.Marshal(envelope)
}
