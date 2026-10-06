package agui

import "encoding/json"

//go:generate python3 internal/generate/generate.py

// DecodeEvent validates the pinned event schema and returns its typed variant.
// Sequence ownership and lifecycle rules must additionally be enforced by the
// consumer: a structurally valid content event need not have an open message.
func DecodeEvent(data []byte) (*WireEvent, error) {
	if err := ValidateEvent(data); err != nil {
		return nil, err
	}
	var result WireEvent
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DecodeInput retains every standard input field, including multimodal message
// bodies, resume entries, metadata, and opaque continuation values.
func DecodeInput(data []byte) (*WireRunAgentInput, error) {
	if err := ValidateInput(data); err != nil {
		return nil, err
	}
	var result WireRunAgentInput
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DecodeCapabilities returns the complete typed capability descriptor. Pointer
// fields distinguish an omitted (unknown) capability from an explicit false.
func DecodeCapabilities(data []byte) (*WireAgentCapabilities, error) {
	if err := ValidateCapabilities(data); err != nil {
		return nil, err
	}
	var result WireAgentCapabilities
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// EncodeEvent validates a constructed typed event before it reaches transport.
// A union's pointer choice does not replace its required wire discriminator.
func EncodeEvent(value *WireEvent) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if err = ValidateEvent(data); err != nil {
		return nil, err
	}
	return data, nil
}

// EncodeInput checks producer-side requiredness and optional non-null values.
func EncodeInput(value *WireRunAgentInput) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if err = ValidateInput(data); err != nil {
		return nil, err
	}
	return data, nil
}

// EncodeCapabilities validates the exact typed discovery descriptor.
func EncodeCapabilities(value *WireAgentCapabilities) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if err = ValidateCapabilities(data); err != nil {
		return nil, err
	}
	return data, nil
}
