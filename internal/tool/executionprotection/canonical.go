package executionprotection

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxSignificantDigits = 1024
	maxAbsoluteExponent  = int64(1_000_000)
)

// CanonicalJSON encodes the JSON data model deterministically.
func CanonicalJSON(value interface{}) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := canonicalEncoder{active: map[canonicalVisit]bool{}}
	if err := encoder.write(&buffer, reflect.ValueOf(value)); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

type canonicalVisit struct {
	kind reflect.Kind
	ptr  uintptr
}

type canonicalEncoder struct {
	active map[canonicalVisit]bool
}

func (e *canonicalEncoder) write(buffer *bytes.Buffer, value reflect.Value) error {
	if !value.IsValid() {
		buffer.WriteString("null")
		return nil
	}
	for value.Kind() == reflect.Interface {
		if value.IsNil() {
			buffer.WriteString("null")
			return nil
		}
		value = value.Elem()
	}
	if value.Type() == reflect.TypeOf(json.Number("")) {
		normalized, err := normalizeJSONNumber(value.Interface().(json.Number).String())
		if err != nil {
			return err
		}
		buffer.WriteString(normalized)
		return nil
	}
	switch value.Kind() {
	case reflect.Bool:
		buffer.WriteString(strconv.FormatBool(value.Bool()))
	case reflect.String:
		if !utf8.ValidString(value.String()) {
			return fmt.Errorf("canonical JSON: invalid UTF-8 string")
		}
		encoded, _ := json.Marshal(value.String())
		buffer.Write(encoded)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		normalized, err := normalizeJSONNumber(strconv.FormatInt(value.Int(), 10))
		if err != nil {
			return err
		}
		buffer.WriteString(normalized)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		normalized, err := normalizeJSONNumber(strconv.FormatUint(value.Uint(), 10))
		if err != nil {
			return err
		}
		buffer.WriteString(normalized)
	case reflect.Float32, reflect.Float64:
		width := value.Type().Bits()
		floating := value.Float()
		if math.IsNaN(floating) || math.IsInf(floating, 0) {
			return fmt.Errorf("canonical JSON: non-finite float")
		}
		normalized, err := normalizeJSONNumber(strconv.FormatFloat(floating, 'g', -1, width))
		if err != nil {
			return err
		}
		buffer.WriteString(normalized)
	case reflect.Map:
		if value.IsNil() {
			buffer.WriteString("null")
			return nil
		}
		if value.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("canonical JSON: object key type %s is not a string", value.Type().Key())
		}
		visit := canonicalVisit{kind: value.Kind(), ptr: value.Pointer()}
		if e.active[visit] {
			return fmt.Errorf("canonical JSON: cyclic object")
		}
		e.active[visit] = true
		defer delete(e.active, visit)
		keys := value.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		buffer.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buffer.WriteByte(',')
			}
			if !utf8.ValidString(key.String()) {
				return fmt.Errorf("canonical JSON: invalid UTF-8 object key")
			}
			encoded, _ := json.Marshal(key.String())
			buffer.Write(encoded)
			buffer.WriteByte(':')
			if err := e.write(buffer, value.MapIndex(key)); err != nil {
				return err
			}
		}
		buffer.WriteByte('}')
	case reflect.Slice:
		if value.IsNil() {
			buffer.WriteString("null")
			return nil
		}
		visit := canonicalVisit{kind: value.Kind(), ptr: value.Pointer()}
		if e.active[visit] {
			return fmt.Errorf("canonical JSON: cyclic array")
		}
		e.active[visit] = true
		defer delete(e.active, visit)
		fallthrough
	case reflect.Array:
		buffer.WriteByte('[')
		for i := 0; i < value.Len(); i++ {
			if i > 0 {
				buffer.WriteByte(',')
			}
			if err := e.write(buffer, value.Index(i)); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
	default:
		return fmt.Errorf("canonical JSON: unsupported value type %s", value.Type())
	}
	return nil
}

func normalizeJSONNumber(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("canonical JSON: empty number")
	}
	negative := false
	if raw[0] == '-' {
		negative = true
		raw = raw[1:]
		if raw == "" {
			return "", fmt.Errorf("canonical JSON: invalid number")
		}
	}
	exponentIndex := strings.IndexAny(raw, "eE")
	mantissa := raw
	exponent := int64(0)
	if exponentIndex >= 0 {
		mantissa = raw[:exponentIndex]
		parsed, err := parseBoundedExponent(raw[exponentIndex+1:])
		if err != nil {
			return "", err
		}
		exponent = parsed
	}
	dot := strings.IndexByte(mantissa, '.')
	integer := mantissa
	fraction := ""
	if dot >= 0 {
		integer = mantissa[:dot]
		fraction = mantissa[dot+1:]
		if strings.IndexByte(fraction, '.') >= 0 || fraction == "" {
			return "", fmt.Errorf("canonical JSON: invalid number")
		}
	}
	if integer == "" || !digitsOnly(integer) || !digitsOnly(fraction) {
		return "", fmt.Errorf("canonical JSON: invalid number")
	}
	if len(integer) > 1 && integer[0] == '0' {
		return "", fmt.Errorf("canonical JSON: invalid leading zero")
	}
	digits := integer + fraction
	first := -1
	last := -1
	for i := range digits {
		if digits[i] != '0' {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return "0", nil
	}
	significant := digits[first : last+1]
	if len(significant) > maxSignificantDigits {
		return "", fmt.Errorf("canonical JSON: number exceeds %d significant digits", maxSignificantDigits)
	}
	normalizedExponent := exponent + int64(len(integer)-first-1)
	if normalizedExponent > maxAbsoluteExponent || normalizedExponent < -maxAbsoluteExponent {
		return "", fmt.Errorf("canonical JSON: exponent exceeds %d", maxAbsoluteExponent)
	}
	var result strings.Builder
	if negative {
		result.WriteByte('-')
	}
	result.WriteByte(significant[0])
	if len(significant) > 1 {
		result.WriteByte('.')
		result.WriteString(significant[1:])
	}
	if normalizedExponent != 0 {
		result.WriteByte('e')
		result.WriteString(strconv.FormatInt(normalizedExponent, 10))
	}
	return result.String(), nil
}

func parseBoundedExponent(raw string) (int64, error) {
	if raw == "" {
		return 0, fmt.Errorf("canonical JSON: invalid exponent")
	}
	sign := int64(1)
	if raw[0] == '+' || raw[0] == '-' {
		if raw[0] == '-' {
			sign = -1
		}
		raw = raw[1:]
	}
	if raw == "" || !digitsOnly(raw) {
		return 0, fmt.Errorf("canonical JSON: invalid exponent")
	}
	value := int64(0)
	for i := range raw {
		value = value*10 + int64(raw[i]-'0')
		if value > maxAbsoluteExponent {
			return 0, fmt.Errorf("canonical JSON: exponent exceeds %d", maxAbsoluteExponent)
		}
	}
	return sign * value, nil
}

func digitsOnly(value string) bool {
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}
