package agui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// JSON Schema integer describes a mathematical value, not a lexical spelling.
// Normalize only schema-declared integer properties before the generated int64
// decoder. Open RawMessage data/metadata never passes through this conversion.
func normalizeWireIntegers(data []byte, keys ...string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for _, key := range keys {
		raw, present := fields[key]
		if !present {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		number, ok := value.(json.Number)
		if !ok {
			continue
		} // preserve ordinary decoder errors/null semantics
		normalized, err := wireSafeInteger(number.String())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		fields[key] = json.RawMessage(normalized)
	}
	return json.Marshal(fields)
}
func wireSafeInteger(number string) (string, error) {
	sign := ""
	if strings.HasPrefix(number, "-") {
		sign = "-"
		number = number[1:]
	}
	mantissa, exponentText := number, "0"
	if i := strings.IndexAny(number, "eE"); i >= 0 {
		mantissa, exponentText = number[:i], number[i+1:]
	}
	decimals := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		decimals = len(mantissa) - i - 1
		mantissa = mantissa[:i] + mantissa[i+1:]
	}
	digits := strings.TrimLeft(mantissa, "0")
	if digits == "" {
		return "0", nil
	} // zero remains zero even with a huge exponent
	trimmed := strings.TrimRight(digits, "0")
	trailing := len(digits) - len(trimmed)
	digits = trimmed
	exponent, err := strconv.ParseInt(exponentText, 10, 64)
	if err != nil {
		return "", fmt.Errorf("integer exceeds the JSON safe range")
	}
	lower := int64(decimals) - int64(trailing)
	if exponent < lower {
		return "", fmt.Errorf("number is not an integer")
	}
	if exponent > lower+16 {
		return "", fmt.Errorf("integer exceeds the JSON safe range")
	}
	zeroes := int(exponent - lower)
	if len(digits)+zeroes > 16 {
		return "", fmt.Errorf("integer exceeds the JSON safe range")
	}
	normalized := sign + digits + strings.Repeat("0", zeroes)
	integer, err := strconv.ParseInt(normalized, 10, 64)
	if err != nil || integer < -9007199254740991 || integer > 9007199254740991 {
		return "", fmt.Errorf("integer exceeds the JSON safe range")
	}
	return strconv.FormatInt(integer, 10), nil
}
