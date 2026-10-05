package aguistate

import (
	"encoding/json"
	"fmt"
)

// EqualJSON reuses RFC 6902's lossless semantic equality, including decimal
// representations and arbitrarily large numbers. It never decodes to float64.
func EqualJSON(left, right json.RawMessage) (bool, error) {
	if !json.Valid(left) || !json.Valid(right) {
		return false, fmt.Errorf("semantic comparison requires two JSON values")
	}
	var a, b any
	if err := decode(left, &a); err != nil {
		return false, err
	}
	if err := decode(right, &b); err != nil {
		return false, err
	}
	return patchEqual(a, b), nil
}
