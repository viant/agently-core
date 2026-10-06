package agui

import "encoding/json"

// UnmarshalJSON retains the complete standard variant while populating the
// original convenience fields when their types match. In particular STATE_DELTA
// arrays and multipart tool results must not fail decoding into the producer's
// older string convenience fields. Full typed access is available via DecodeEvent.
func (event *Event) UnmarshalJSON(raw []byte) error {
	if err := ValidateEvent(raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for _, key := range []string{"delta", "content"} {
		if value, ok := fields[key]; ok {
			var text string
			if json.Unmarshal(value, &text) != nil {
				delete(fields, key)
			}
		}
	}
	filtered, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	filtered, err = normalizeWireIntegers(filtered, "timestamp")
	if err != nil {
		return err
	}
	type alias Event
	var decoded alias
	if err = json.Unmarshal(filtered, &decoded); err != nil {
		return err
	}
	*event = Event(decoded)
	event.Standard = append(json.RawMessage(nil), raw...)
	return nil
}

func (event Event) MarshalJSON() ([]byte, error) {
	type alias Event
	if event.Standard == nil {
		return json.Marshal(alias(event))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(event.Standard, &fields); err != nil {
		return nil, err
	}
	if event.Type != "" {
		fields["type"], _ = json.Marshal(event.Type)
	}
	if event.Timestamp != 0 {
		fields["timestamp"], _ = json.Marshal(event.Timestamp)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	if err = ValidateEvent(raw); err != nil {
		return nil, err
	}
	return raw, nil
}
func StandardEvent(raw json.RawMessage) (Event, error) {
	if err := ValidateEvent(raw); err != nil {
		return Event{}, err
	}
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return Event{}, err
	}
	return Event{Type: header.Type, Standard: append(json.RawMessage(nil), raw...)}, nil
}
func standardEvent(fields map[string]any) Event {
	raw, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	return Event{Type: fields["type"].(string), Standard: raw, applied: true}
}
