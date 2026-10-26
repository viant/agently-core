package forecastbinding

import (
	"context"
	"encoding/json"
	"reflect"
	"time"
)

// ProjectionPolicyProducer is a synthetic test adapter for the documented
// report evidence envelope. Production hosts provide their own PolicyProducer.
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
		EntityIDs         []int64                    `json:"entityIds"`
		SourceProfileHash string                     `json:"sourceProfileHash"`
		Inclusion         string                     `json:"inclusion"`
		Exclusion         string                     `json:"exclusion"`
		Templates         map[string]json.RawMessage `json:"templates"`
	}
	if e = json.Unmarshal(raw, &projection); e != nil || projection.Version != 1 || projection.ProducerVersion == "" {
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
	injectedID, err := json.Marshal(args["evidenceEntityId"])
	var selectedID int64
	if err != nil || json.Unmarshal(injectedID, &selectedID) != nil || len(projection.EntityIDs) != 1 || selectedID != projection.EntityIDs[0] {
		return nil, reject("conversion selected profile mismatch")
	}
	profileEntity, ok := row["entity"].(map[string]any)
	if !ok {
		return nil, reject("profile entity missing")
	}
	if args["inclusion"] != profileEntity["target"] || args["exclusion"] != profileEntity["exclusion"] || args["inclusion"] != projection.Inclusion || args["exclusion"] != projection.Exclusion {
		return nil, reject("conversion selection differs from profile")
	}
	profileRequest, e := object(s.Profile.Request)
	if e != nil {
		return nil, e
	}
	idRaw, _ := json.Marshal(profileRequest["EntityId"])
	var ids []int64
	if e = json.Unmarshal(idRaw, &ids); e != nil || !reflect.DeepEqual(ids, projection.EntityIDs) {
		return nil, reject("profile selected entity mismatch")
	}
	idRaw, _ = json.Marshal(row["entityId"])
	var entityID int64
	if e = json.Unmarshal(idRaw, &entityID); e != nil || len(ids) != 1 || ids[0] != entityID {
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
	out := &ProducedPolicy{Policy: Policy{SourceTool: s.Tools.Data, Dates: days, Templates: projection.Templates}, EntityIDs: projection.EntityIDs, ProducerVersion: projection.ProducerVersion}
	out.ProfileRequestHash, _ = RequestHash(s.Profile.Request)
	out.ProfileResponseHash, _ = RequestHash(s.Profile.Response)
	out.ConversionRequestHash, _ = RequestHash(s.Conversion.Request)
	out.ConversionResponseHash, _ = RequestHash(s.Conversion.Response)
	return out, nil
}
