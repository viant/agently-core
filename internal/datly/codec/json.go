package codec

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
)

// JSON is a logical elicitation payload populated by reader hooks.
type JSON map[string]interface{}

// Scan preserves NULL and empty logical payloads until relation hooks hydrate
// elicitation data. This is the business codec, not a database execution path.
func (value *JSON) Scan(source any) error {
	if value == nil {
		return nil
	}
	if source == nil {
		*value = nil
		return nil
	}
	var raw string
	switch source := source.(type) {
	case string:
		raw = source
	case []byte:
		raw = string(source)
	default:
		return fmt.Errorf("unsupported JSON scan type %T", source)
	}
	if strings.TrimSpace(raw) == "" {
		*value = nil
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return err
	}
	*value = JSON(decoded)
	return nil
}

func (value JSON) Value() (driver.Value, error) {
	if len(value) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(map[string]any(value))
	if err != nil {
		return nil, err
	}
	return string(encoded), nil
}
