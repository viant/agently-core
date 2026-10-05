package forecastbinding

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"time"
)

const ProjectionKey = "forecastEvidence"
const ReceiptKey = "_agentlyEvidence"
const PlanReceiptKey = "_agentlyForecastPlan"

type Plan struct {
	ID             string         `json:"id"`
	Admission      Admission      `json:"admission"`
	Produced       ProducedPolicy `json:"produced"`
	ProfileOpID    string         `json:"profileOpId"`
	ConversionOpID string         `json:"conversionOpId"`
}

func (a Admission) Validate() error {
	if a.OwnerID == "" || a.ConversationID == "" || a.TurnID == "" || a.StarterMessageID == "" || a.ReceivedAt.IsZero() {
		return reject("incomplete admission")
	}
	if _, e := time.LoadLocation(a.TimeZone); e != nil {
		return reject("invalid admission timezone")
	}
	switch a.SelectionOrigin {
	case SelectionUser:
		if len(a.AudienceIDs) != 1 || a.AudienceIDs[0] <= 0 {
			return reject("invalid user selection")
		}
	case SelectionToolEvidence:
		if len(a.AudienceIDs) != 0 {
			return reject("tool evidence cannot claim user selection")
		}
	default:
		return reject("invalid selection provenance")
	}
	switch a.DateOrigin {
	case DateUserWindow, DateWorkspaceDefault:
		if len(a.Dates) == 0 {
			return reject("missing admitted dates")
		}
	case DateToolEvidence:
		if len(a.Dates) != 0 {
			return reject("tool date evidence cannot claim user window")
		}
	default:
		return reject("invalid date provenance")
	}
	seen := map[string]bool{}
	for _, day := range a.Dates {
		v, e := time.Parse("2006-01-02", day)
		if e != nil || v.Format("2006-01-02") != day || seen[day] {
			return reject("invalid admission dates")
		}
		seen[day] = true
	}
	return nil
}
func (p *Plan) ValidateIdentity() error {
	if p == nil {
		return reject("missing plan")
	}
	if e := p.Admission.Validate(); e != nil {
		return e
	}
	copy := *p
	copy.ID = ""
	raw, _ := json.Marshal(copy)
	id, e := RequestHash(raw)
	if e != nil || p.ID != id {
		return reject("plan identity mismatch")
	}
	return nil
}
func NewPlan(ctx context.Context, producer PolicyProducer, a Admission, sources PlanSources) (*Plan, error) {
	if e := a.Validate(); e != nil {
		return nil, e
	}
	if producer == nil {
		return nil, reject("authoritative producer unavailable")
	}
	for _, c := range []Call{sources.Profile, sources.Conversion} {
		if c.Scope != a.Scope || c.Status != "completed" || c.OpID == "" {
			return nil, reject("plan source scope or status mismatch")
		}
	}
	if sources.Profile.Tool != "steward/AdTargetingProfile" || sources.Conversion.Tool != "steward/ForecastingTargetingConvert" {
		return nil, reject("wrong plan source tools")
	}
	out, e := producer.Produce(ctx, a, sources)
	if e != nil {
		return nil, e
	}
	if out == nil || out.ProducerVersion == "" || len(out.Policy.Dates) == 0 || len(out.Policy.Templates) == 0 || len(out.AudienceIDs) != 1 {
		return nil, reject("incomplete produced policy")
	}
	if a.SelectionOrigin == SelectionUser && !reflect.DeepEqual(a.AudienceIDs, out.AudienceIDs) {
		return nil, reject("explicit selection mismatch")
	}
	if a.DateOrigin != DateToolEvidence {
		x := append([]string(nil), a.Dates...)
		y := append([]string(nil), out.Policy.Dates...)
		sort.Strings(x)
		sort.Strings(y)
		if !reflect.DeepEqual(x, y) {
			return nil, reject("explicit date mismatch")
		}
	}
	for _, v := range []struct {
		raw  json.RawMessage
		hash string
	}{{sources.Profile.Request, out.ProfileRequestHash}, {sources.Profile.Response, out.ProfileResponseHash}, {sources.Conversion.Request, out.ConversionRequestHash}, {sources.Conversion.Response, out.ConversionResponseHash}} {
		h, e := RequestHash(v.raw)
		if e != nil || h != v.hash {
			return nil, reject("producer source hash mismatch")
		}
	}
	p := &Plan{Admission: a, Produced: *out, ProfileOpID: sources.Profile.OpID, ConversionOpID: sources.Conversion.OpID}
	raw, _ := json.Marshal(p)
	p.ID, _ = RequestHash(raw)
	// Deep-copy all caller-owned maps and raw byte slices before publishing a plan.
	encoded, _ := json.Marshal(p)
	var frozen Plan
	if e = json.Unmarshal(encoded, &frozen); e != nil {
		return nil, e
	}
	return &frozen, nil
}

// ProjectionPolicyProducer consumes the authoritative private producer's wire
// projection, validating its source profile and conversion before admitting it.
// It contains no targeting converter or selector-name mapping of its own.
type ProjectionPolicyProducer struct{}

func (ProjectionPolicyProducer) Produce(_ context.Context, a Admission, s PlanSources) (*ProducedPolicy, error) {
	profile, e := object(s.Profile.Response)
	if e != nil {
		return nil, e
	}
	data, ok := profile["data"].([]any)
	if profile["status"] != "ok" || !ok || len(data) != 1 {
		return nil, reject("expected one successful profile")
	}
	row, ok := data[0].(map[string]any)
	if !ok {
		return nil, reject("invalid profile row")
	}
	profileRaw, _ := json.Marshal(row)
	conversion, e := object(s.Conversion.Response)
	if e != nil || conversion["status"] != "ok" {
		return nil, reject("unsuccessful conversion")
	}
	raw, e := json.Marshal(conversion[ProjectionKey])
	if e != nil {
		return nil, e
	}
	var projection struct {
		Version           int                        `json:"version"`
		ProducerVersion   string                     `json:"producerVersion"`
		AudienceIDs       []int64                    `json:"audienceIds"`
		SourceProfileHash string                     `json:"sourceProfileHash"`
		Inclusion         string                     `json:"inclusion"`
		Exclusion         string                     `json:"exclusion"`
		Templates         map[string]json.RawMessage `json:"templates"`
	}
	if e = json.Unmarshal(raw, &projection); e != nil || projection.Version != 1 || projection.ProducerVersion != "steward-forecast-profile-v1" {
		return nil, reject("authoritative projection missing or unsupported")
	}
	h, _ := RequestHash(profileRaw)
	if h != projection.SourceProfileHash {
		return nil, reject("projection profile mismatch")
	}
	request, e := object(s.Conversion.Request)
	if e != nil {
		return nil, e
	}
	args, ok := request["Request"].(map[string]any)
	if !ok {
		return nil, reject("conversion request missing")
	}
	injectedRaw, err := json.Marshal(args["evidenceProfile"])
	if err != nil {
		return nil, err
	}
	injectedHash, err := RequestHash(injectedRaw)
	if err != nil || injectedHash != projection.SourceProfileHash {
		return nil, reject("conversion lacks exact source profile projection")
	}
	injectedID, err := json.Marshal(args["evidenceAudienceId"])
	var selectedID int64
	if err != nil || json.Unmarshal(injectedID, &selectedID) != nil || len(projection.AudienceIDs) != 1 || selectedID != projection.AudienceIDs[0] {
		return nil, reject("conversion selected profile mismatch")
	}
	audience, ok := row["audience"].(map[string]any)
	if !ok {
		return nil, reject("profile audience missing")
	}
	if args["inclusion"] != audience["target"] || args["exclusion"] != audience["exclusion"] || args["inclusion"] != projection.Inclusion || args["exclusion"] != projection.Exclusion {
		return nil, reject("conversion targeting differs from profile")
	}
	profileRequest, e := object(s.Profile.Request)
	if e != nil {
		return nil, e
	}
	idRaw, _ := json.Marshal(profileRequest["AudienceId"])
	var ids []int64
	if e = json.Unmarshal(idRaw, &ids); e != nil || !reflect.DeepEqual(ids, projection.AudienceIDs) {
		return nil, reject("profile selected entity mismatch")
	}
	idRaw, _ = json.Marshal(row["audienceId"])
	var entity int64
	if e = json.Unmarshal(idRaw, &entity); e != nil || len(ids) != 1 || ids[0] != entity {
		return nil, reject("profile entity identity mismatch")
	}
	from, ok := args["from"].(string)
	if !ok {
		return nil, reject("conversion window missing")
	}
	to, ok := args["to"].(string)
	if !ok {
		return nil, reject("conversion window missing")
	}
	first, e := time.Parse("2006-01-02", from)
	if e != nil {
		return nil, e
	}
	last, e := time.Parse("2006-01-02", to)
	if e != nil || last.Before(first) || last.Sub(first) > 366*24*time.Hour {
		return nil, reject("unsupported conversion window")
	}
	body, ok := conversion["body"].(map[string]any)
	if !ok || body["from"] != from || body["to"] != to {
		return nil, reject("conversion response window mismatch")
	}
	var days []string
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format("2006-01-02"))
	}
	out := &ProducedPolicy{Policy: Policy{Dates: days, Templates: projection.Templates}, AudienceIDs: projection.AudienceIDs, ProducerVersion: projection.ProducerVersion}
	out.ProfileRequestHash, _ = RequestHash(s.Profile.Request)
	out.ProfileResponseHash, _ = RequestHash(s.Profile.Response)
	out.ConversionRequestHash, _ = RequestHash(s.Conversion.Request)
	out.ConversionResponseHash, _ = RequestHash(s.Conversion.Response)
	return out, nil
}
