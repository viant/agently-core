// Package forecastbinding materializes forecast rows from immutable completed
// evidence. It deliberately has no store, network, or tool-dispatch dependency.
package forecastbinding

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"time"
)

const Profile = "forecast-daily-v1"
const AvailsPointer = "/data/0/avails"

type Scope struct {
	OwnerID        string `json:"ownerId"`
	ConversationID string `json:"conversationId"`
	TurnID         string `json:"turnId"`
}
type Call struct {
	Scope
	MessageID string          `json:"messageId,omitempty"`
	OpID      string          `json:"opId"`
	Tool      string          `json:"tool"`
	Status    string          `json:"status"`
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response"`
}

// Policy is supplied by the authorized host, never decoded from model bindings.
// Templates include every expected request field except filters.date.
type Policy struct {
	Dates     []string                   `json:"dates"`
	Templates map[string]json.RawMessage `json:"templates"`
}
type Reference struct {
	OpID            string `json:"opId"`
	RequestHash     string `json:"requestHash"`
	ResponsePointer string `json:"responsePointer"`
}
type Column struct {
	Key   string      `json:"key"`
	Calls []Reference `json:"calls"`
}
type Bindings struct {
	Profile string   `json:"profile"`
	Columns []Column `json:"columns"`
}
type CellProvenance struct {
	Date   string `json:"date"`
	Column string `json:"column"`
	Reference
}
type Result struct {
	Data       json.RawMessage  `json:"data"`
	Provenance []CellProvenance `json:"provenance"`
}

func decode(raw json.RawMessage) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	return value, nil
}
func object(raw json.RawMessage) (map[string]any, error) {
	v, e := decode(raw)
	if e != nil {
		return nil, e
	}
	o, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object")
	}
	return o, nil
}
func canonical(raw json.RawMessage) ([]byte, error) {
	v, e := decode(raw)
	if e != nil {
		return nil, e
	}
	return json.Marshal(v)
}
func RequestHash(raw json.RawMessage) (string, error) {
	b, e := canonical(raw)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func reject(reason string) error { return fmt.Errorf("forecast evidence: %s", reason) }

func Materialize(scope Scope, policy Policy, bindings Bindings, records []Call) (*Result, error) {
	if scope.OwnerID == "" || scope.ConversationID == "" || scope.TurnID == "" {
		return nil, reject("missing trusted scope")
	}
	if bindings.Profile != Profile {
		return nil, reject("unsupported profile")
	}
	if len(policy.Dates) == 0 || len(policy.Templates) == 0 {
		return nil, reject("missing trusted policy")
	}
	days := map[string]bool{}
	for _, day := range policy.Dates {
		t, e := time.Parse("2006-01-02", day)
		if e != nil || t.Format("2006-01-02") != day || days[day] {
			return nil, reject("invalid or duplicate policy date")
		}
		days[day] = true
	}
	byID := map[string]Call{}
	for _, call := range records {
		if call.OpID == "" {
			return nil, reject("missing evidence ID")
		}
		if _, exists := byID[call.OpID]; exists {
			return nil, reject("duplicate evidence ID")
		}
		byID[call.OpID] = call
	}
	rows := map[string]map[string]any{}
	for day := range days {
		rows[day] = map[string]any{"date": day}
	}
	var proof []CellProvenance
	columns := map[string]bool{}
	for _, column := range bindings.Columns {
		template, exists := policy.Templates[column.Key]
		if !exists || column.Key == "" || column.Key == "date" || columns[column.Key] {
			return nil, reject("unknown or duplicate column")
		}
		columns[column.Key] = true
		expected, e := object(template)
		if e != nil {
			return nil, reject("invalid policy template")
		}
		filters, ok := expected["filters"].(map[string]any)
		if !ok {
			return nil, reject("missing policy filters")
		}
		if _, hasDate := filters["date"]; hasDate {
			return nil, reject("policy template contains date")
		}
		dimensions, ok := expected["dimensions"].(map[string]any)
		if !ok || len(dimensions) != 0 {
			return nil, reject("unsupported policy dimensions")
		}
		measures, ok := expected["measures"].(map[string]any)
		if !ok || measures["avails"] != true {
			return nil, reject("missing policy avails measure")
		}
		boundDays := map[string]bool{}
		for _, ref := range column.Calls {
			call, exists := byID[ref.OpID]
			if !exists {
				return nil, reject("missing evidence")
			}
			if call.Scope != scope {
				return nil, reject("evidence scope mismatch")
			}
			if call.Tool != "steward/ForecastingCube" || call.Status != "completed" {
				return nil, reject("unsupported or incomplete evidence")
			}
			hash, e := RequestHash(call.Request)
			if e != nil || hash != ref.RequestHash {
				return nil, reject("request hash mismatch")
			}
			if ref.ResponsePointer != AvailsPointer {
				return nil, reject("unsupported response pointer")
			}
			request, e := object(call.Request)
			if e != nil {
				return nil, reject("invalid evidence request")
			}
			requestFilters, ok := request["filters"].(map[string]any)
			if !ok {
				return nil, reject("missing evidence filters")
			}
			date, ok := requestFilters["date"].(string)
			if !ok {
				return nil, reject("missing request date")
			}
			t, e := time.Parse(time.RFC3339, date)
			if e != nil || date != t.UTC().Format("2006-01-02T00:00:00Z") || t.Hour() != 0 || t.Minute() != 0 || t.Second() != 0 || t.Nanosecond() != 0 {
				return nil, reject("unsupported request date")
			}
			day := t.UTC().Format("2006-01-02")
			if !days[day] || boundDays[day] {
				return nil, reject("unknown or duplicate bound date")
			}
			boundDays[day] = true
			filters["date"] = date
			expectedJSON, _ := json.Marshal(expected)
			actualJSON, _ := json.Marshal(request)
			if !bytes.Equal(expectedJSON, actualJSON) {
				return nil, reject("request policy mismatch")
			}
			delete(filters, "date")
			response, e := object(call.Response)
			if e != nil || response["status"] != "ok" {
				return nil, reject("unsuccessful evidence response")
			}
			if continuation, ok := response["continuation"].(map[string]any); ok {
				remaining, _ := continuation["remaining"].(json.Number)
				if continuation["hasMore"] == true || (remaining != "" && remaining != "0") {
					return nil, reject("incomplete continuation evidence")
				}
			}
			data, ok := response["data"].([]any)
			if !ok || len(data) != 1 {
				return nil, reject("expected one aggregate row")
			}
			row, ok := data[0].(map[string]any)
			if !ok {
				return nil, reject("invalid aggregate row")
			}
			if eventDate, exists := row["eventDate"]; exists && eventDate != nil && eventDate != date {
				return nil, reject("response date conflicts with request")
			}
			number, ok := row["avails"].(json.Number)
			if !ok {
				return nil, reject("missing numeric avails")
			}
			n, e := strconv.ParseFloat(number.String(), 64)
			if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
				return nil, reject("invalid numeric avails")
			}
			rows[day][column.Key] = number
			proof = append(proof, CellProvenance{Date: day, Column: column.Key, Reference: ref})
		}
		if len(boundDays) != len(days) {
			return nil, reject("missing bound date")
		}
	}
	if len(columns) != len(policy.Templates) {
		return nil, reject("missing column")
	}
	orderedDays := append([]string(nil), policy.Dates...)
	sort.Strings(orderedDays)
	output := make([]map[string]any, 0, len(orderedDays))
	for _, day := range orderedDays {
		output = append(output, rows[day])
	}
	sort.Slice(proof, func(i, j int) bool {
		if proof[i].Date != proof[j].Date {
			return proof[i].Date < proof[j].Date
		}
		return proof[i].Column < proof[j].Column
	})
	data, e := json.Marshal(output)
	if e != nil {
		return nil, e
	}
	return &Result{Data: data, Provenance: proof}, nil
}

// Validate never repairs an existing body. A mismatch is an error, not permission
// to overwrite a historical report or silently relabel model-authored rows.
func Validate(scope Scope, policy Policy, bindings Bindings, records []Call, proposed json.RawMessage) (*Result, error) {
	result, e := Materialize(scope, policy, bindings, records)
	if e != nil {
		return nil, e
	}
	actual, e := canonical(proposed)
	if e != nil || !bytes.Equal(actual, result.Data) {
		return nil, reject("authored data disagrees with bound evidence")
	}
	return result, nil
}
