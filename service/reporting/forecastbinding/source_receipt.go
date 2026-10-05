package forecastbinding

import (
	"context"
	"encoding/json"
	"time"
)

const SourceReceiptKey = "_agentlySource"
const ProfileReceiptKey = "_agentlyForecastProfile"

// SourceReceipt describes executed evidence, not a claim that a request matches
// any plan or user intent. The publication binder checks those independently.
type SourceReceipt struct {
	Profile         string `json:"profile"`
	OpID            string `json:"opId"`
	RequestHash     string `json:"requestHash"`
	ResponseHash    string `json:"responseHash"`
	Date            string `json:"date,omitempty"`
	ResponsePointer string `json:"responsePointer,omitempty"`
}

func sourceProjection(call *Call) (json.RawMessage, error) {
	response, err := object(call.Response)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{ReceiptKey, PlanReceiptKey, SourceReceiptKey, ProfileReceiptKey} {
		if _, exists := response[key]; exists {
			return nil, reject("reserved evidence key collision")
		}
	}
	requestHash, err := RequestHash(call.Request)
	if err != nil {
		return nil, err
	}
	responseHash, err := RequestHash(call.Response)
	if err != nil {
		return nil, err
	}
	receipt := SourceReceipt{Profile: Profile, OpID: call.OpID, RequestHash: requestHash, ResponseHash: responseHash}
	key := ProfileReceiptKey
	switch call.Tool {
	case "steward/AdTargetingProfile":
		// This is an identity receipt only. PrepareConversion validates the profile's
		// selected entity and target before the profile can become policy authority.
	case "steward/ForecastingCube":
		request, err := object(call.Request)
		if err != nil {
			return nil, err
		}
		filters, ok := request["filters"].(map[string]any)
		if !ok {
			return nil, reject("missing source filters")
		}
		if date, ok := filters["date"].(string); ok {
			if timestamp, parseErr := time.Parse(time.RFC3339, date); parseErr == nil {
				receipt.Date = timestamp.Format("2006-01-02")
			}
		}
		if receipt.Date != "" {
			delete(filters, "date")
			template, _ := json.Marshal(request)
			_, eligible := Materialize(call.Scope, Policy{Dates: []string{receipt.Date}, Templates: map[string]json.RawMessage{"source": template}}, Bindings{Profile: Profile, Columns: []Column{{Key: "source", Calls: []Reference{{OpID: call.OpID, RequestHash: requestHash, ResponsePointer: AvailsPointer}}}}}, []Call{*call})
			if eligible == nil {
				receipt.ResponsePointer = AvailsPointer
			}
		}
		// General cube reads (including grouped/range queries) remain valid
		// evidence. A pointer is advertised only for a valid scalar aggregate. The later
		// plan binder still authorizes its exact category and request scope.
		key = SourceReceiptKey
	default:
		return nil, reject("unsupported source receipt tool")
	}
	response[key] = receipt
	return json.Marshal(response)
}

// ProjectCompleted releases receipts from exact durable calls. A converter
// receipt first saves and rereads its immutable plan under the current lease.
func (r *Runtime) ProjectCompleted(ctx context.Context, scope Scope, op, lease string) (json.RawMessage, error) {
	call, err := r.store.LoadCompletedCall(ctx, scope, op)
	if err != nil {
		return nil, err
	}
	if call == nil || call.Scope != scope || call.Status != "completed" {
		return nil, reject("source completion unconfirmed")
	}
	var body json.RawMessage
	if call.Tool == "steward/ForecastingTargetingConvert" {
		request, err := object(call.Request)
		if err != nil {
			return nil, err
		}
		args, ok := request["Request"].(map[string]any)
		if !ok {
			return nil, reject("conversion request missing")
		}
		profileOp, ok := args[trustedProfileReference].(string)
		if !ok || profileOp == "" {
			return nil, reject("conversion lacks trusted source reference")
		}
		plan, err := r.AdmitPlan(ctx, scope, profileOp, op, lease)
		if err != nil {
			return nil, err
		}
		body, err = r.planProjection(ctx, call, plan.ID)
		if err != nil {
			return nil, err
		}
	} else {
		body, err = sourceProjection(call)
		if err != nil {
			return nil, err
		}
	}
	writer, ok := r.store.(ProjectionStore)
	if !ok {
		return nil, reject("durable receipt projection unavailable")
	}
	if err = writer.PublishCompletedProjection(ctx, scope, op, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (r *Runtime) planProjection(ctx context.Context, call *Call, planID string) (json.RawMessage, error) {
	plan, err := r.store.LoadPlan(ctx, call.Scope, planID)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.ConversionOpID != call.OpID || plan.Admission.Scope != call.Scope {
		return nil, reject("plan conversion mismatch")
	}
	requestHash, err := RequestHash(call.Request)
	if err != nil {
		return nil, err
	}
	responseHash, err := RequestHash(call.Response)
	if err != nil {
		return nil, err
	}
	if requestHash != plan.Produced.ConversionRequestHash || responseHash != plan.Produced.ConversionResponseHash {
		return nil, reject("plan conversion payload mismatch")
	}
	receipt, err := r.AdvertisePlan(ctx, call.Scope, planID)
	if err != nil {
		return nil, err
	}
	response, err := object(call.Response)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{ReceiptKey, PlanReceiptKey, SourceReceiptKey, ProfileReceiptKey} {
		if _, exists := response[key]; exists {
			return nil, reject("reserved evidence key collision")
		}
	}
	response[PlanReceiptKey] = receipt
	return json.Marshal(response)
}
