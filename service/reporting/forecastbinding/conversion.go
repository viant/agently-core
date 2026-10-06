package forecastbinding

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// PrepareConversion injects only the independently loaded profile into a new
// effective conversion request. It neither executes the converter nor trusts a
// model-supplied profile. Original request bytes remain unchanged.
func (r *Runtime) PrepareConversion(ctx context.Context, scope Scope, profileOp string, raw json.RawMessage) (json.RawMessage, error) {
	a, e := r.store.LoadAdmission(ctx, scope)
	if e != nil {
		return nil, e
	}
	if a == nil || a.Scope != scope {
		return nil, reject("admission scope mismatch")
	}
	if e = a.Validate(); e != nil {
		return nil, e
	}
	c, e := r.store.LoadCompletedCall(ctx, scope, profileOp)
	if e != nil {
		return nil, e
	}
	return prepareConversion(a, c, scope, raw)
}

func prepareConversion(a *Admission, c *Call, scope Scope, raw json.RawMessage) (json.RawMessage, error) {
	if c == nil || c.Scope != scope || c.Status != "completed" || c.Tool != "steward/AdTargetingProfile" {
		return nil, reject("authorized profile unavailable")
	}
	response, e := object(c.Response)
	if e != nil {
		return nil, e
	}
	rows, ok := response["data"].([]any)
	if response["status"] != "ok" || !ok || len(rows) != 1 {
		return nil, reject("ambiguous profile")
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		return nil, reject("invalid profile")
	}
	idRaw, _ := json.Marshal(row["audienceId"])
	var id int64
	if e = json.Unmarshal(idRaw, &id); e != nil || id <= 0 {
		return nil, reject("profile audience missing")
	}
	if a.SelectionOrigin == SelectionUser && !reflect.DeepEqual(a.AudienceIDs, []int64{id}) {
		return nil, reject("explicit selection mismatch")
	}
	profileRequest, e := object(c.Request)
	if e != nil {
		return nil, e
	}
	idsRaw, _ := json.Marshal(profileRequest["AudienceId"])
	var ids []int64
	if e = json.Unmarshal(idsRaw, &ids); e != nil || !reflect.DeepEqual(ids, []int64{id}) {
		return nil, reject("profile request identity mismatch")
	}
	audience, ok := row["audience"].(map[string]any)
	if !ok {
		return nil, reject("profile audience missing")
	}
	nestedRaw, _ := json.Marshal(audience["id"])
	var nestedID int64
	if e = json.Unmarshal(nestedRaw, &nestedID); e != nil || nestedID != id {
		return nil, reject("profile nested entity mismatch")
	}
	request, e := object(raw)
	if e != nil {
		return nil, e
	}
	args, ok := request["Request"].(map[string]any)
	if !ok {
		return nil, reject("conversion request missing")
	}
	for _, key := range []string{"evidenceProfile", "evidenceAudienceId"} {
		if _, exists := args[key]; exists {
			return nil, reject("model cannot supply trusted profile body")
		}
	}
	for _, pair := range [][2]string{{"inclusion", "target"}, {"exclusion", "exclusion"}} {
		expected, _ := audience[pair[1]].(string)
		if supplied, exists := args[pair[0]]; exists && supplied != nil && textValue(supplied) != strings.TrimSpace(expected) {
			return nil, reject("conversion target differs from selected profile")
		}
		args[pair[0]] = expected
	}
	if a.DateOrigin != DateToolEvidence {
		days := append([]string(nil), a.Dates...)
		sort.Strings(days)
		for key, expected := range map[string]string{"from": days[0], "to": days[len(days)-1]} {
			if supplied, exists := args[key]; exists && supplied != expected {
				return nil, reject("conversion differs from admitted dates")
			}
			args[key] = expected
		}
	}
	args["evidenceProfile"] = row
	args["evidenceAudienceId"] = id
	return json.Marshal(request)
}

type PlanReceipt struct {
	Profile         string            `json:"profile"`
	PlanID          string            `json:"planId"`
	Dates           []string          `json:"dates"`
	AudienceIDs     []int64           `json:"audienceIds"`
	SelectionOrigin SelectionOrigin   `json:"selectionOrigin"`
	DateOrigin      DateOrigin        `json:"dateOrigin"`
	Expected        []ExpectedRequest `json:"expectedRequests"`
}
type ExpectedRequest struct {
	Column      string          `json:"column"`
	Date        string          `json:"date"`
	RequestHash string          `json:"requestHash"`
	Request     json.RawMessage `json:"request"`
}

// AdvertisePlan only uses a confirmed immutable saved plan, supplying the
// exact request hashes/templates to the model instead of asking it to compute them.
func (r *Runtime) AdvertisePlan(ctx context.Context, scope Scope, id string) (*PlanReceipt, error) {
	p, e := r.store.LoadPlan(ctx, scope, id)
	if e != nil {
		return nil, e
	}
	if p == nil || p.Admission.Scope != scope {
		return nil, reject("plan scope mismatch")
	}
	if e = p.ValidateIdentity(); e != nil {
		return nil, e
	}
	result := &PlanReceipt{Profile: Profile, PlanID: id, Dates: append([]string(nil), p.Produced.Policy.Dates...), AudienceIDs: append([]int64(nil), p.Produced.AudienceIDs...), SelectionOrigin: p.Admission.SelectionOrigin, DateOrigin: p.Admission.DateOrigin}
	sort.Strings(result.Dates)
	var keys []string
	for key := range p.Produced.Policy.Templates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, day := range result.Dates {
			request, e := object(p.Produced.Policy.Templates[key])
			if e != nil {
				return nil, e
			}
			filters, ok := request["filters"].(map[string]any)
			if !ok {
				return nil, reject("policy filters missing")
			}
			filters["date"] = day + "T00:00:00Z"
			raw, e := json.Marshal(request)
			if e != nil {
				return nil, e
			}
			hash, e := RequestHash(raw)
			if e != nil {
				return nil, e
			}
			result.Expected = append(result.Expected, ExpectedRequest{Column: key, Date: day, RequestHash: hash, Request: raw})
		}
	}
	return result, nil
}

func textValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return "\x00invalid"
	}
	return strings.TrimSpace(text)
}
