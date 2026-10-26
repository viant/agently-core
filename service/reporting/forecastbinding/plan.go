package forecastbinding

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"time"
)

const ProjectionKey = "reportEvidence"
const ReceiptKey = "_agentlyEvidence"
const PlanReceiptKey = "_agentlyReportPlan"

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
		if len(a.EntityIDs) != 1 || a.EntityIDs[0] <= 0 {
			return reject("invalid user selection")
		}
	case SelectionToolEvidence:
		if len(a.EntityIDs) != 0 {
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
	tools := sources.Tools
	if e := tools.Validate(); e != nil {
		return nil, e
	}
	for _, c := range []Call{sources.Profile, sources.Conversion} {
		if c.Scope != a.Scope || c.Status != "completed" || c.OpID == "" {
			return nil, reject("plan source scope or status mismatch")
		}
	}
	if !tools.isProfile(sources.Profile.Tool) || !tools.isConversion(sources.Conversion.Tool) {
		return nil, reject("wrong plan source tools")
	}
	out, e := producer.Produce(ctx, a, sources)
	if e != nil {
		return nil, e
	}
	if out == nil || out.ProducerVersion == "" || !tools.isData(out.Policy.SourceTool) || len(out.Policy.Dates) == 0 || len(out.Policy.Templates) == 0 || len(out.EntityIDs) != 1 {
		return nil, reject("incomplete produced policy")
	}
	if a.SelectionOrigin == SelectionUser && !reflect.DeepEqual(a.EntityIDs, out.EntityIDs) {
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
