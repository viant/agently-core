package agui

import (
	"bytes"
	"encoding/json"
)

// Message keeps the complete schema-validated snapshot while retaining the
// convenient fields used by native boundary code. Unknown optional standard
// fields and explicitly empty optional values must not disappear on a resume.
func (m *Message) UnmarshalJSON(data []byte) error {
	type plain Message
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*m = Message(decoded)
	m.rawInput = append(json.RawMessage(nil), data...)
	return nil
}
func (m Message) MarshalJSON() ([]byte, error) {
	type plain Message
	encoded, err := json.Marshal(plain(m))
	if err != nil || len(m.rawInput) == 0 {
		return encoded, err
	}
	var original plain
	if err = json.Unmarshal(m.rawInput, &original); err != nil {
		return nil, err
	}
	baseline, err := json.Marshal(original)
	if err != nil {
		return nil, err
	}
	var raw, oldFields, newFields map[string]json.RawMessage
	if err = json.Unmarshal(m.rawInput, &raw); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(baseline, &oldFields); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(encoded, &newFields); err != nil {
		return nil, err
	}
	for key, old := range oldFields {
		value, present := newFields[key]
		if !present {
			delete(raw, key)
		} else if !bytes.Equal(value, old) {
			raw[key] = value
		}
	}
	for key, value := range newFields {
		if _, present := oldFields[key]; !present {
			raw[key] = value
		}
	}
	return json.Marshal(raw)
}
