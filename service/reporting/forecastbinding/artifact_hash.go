package forecastbinding

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash"
	"math/big"
	"sort"
	"strings"
)

// ArtifactHash permits ordinary JSON encoder spelling differences (1, 1.0,
// 1e0), without float64 rounding that could hide a changed large integer. It is
// separate from the lexical exact-request hash used by source bindings.
func ArtifactHash(raw json.RawMessage) (string, error) {
	if !json.Valid(raw) {
		return "", reject("invalid artifact JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	digest := sha256.New()
	digest.Write([]byte("agently.report.artifact.v1"))
	if err := writeArtifactValue(digest, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
func artifactLength(digest hash.Hash, size int) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(size))
	digest.Write(length[:])
}
func artifactText(digest hash.Hash, tag byte, value string) {
	digest.Write([]byte{tag})
	artifactLength(digest, len(value))
	digest.Write([]byte(value))
}
func writeArtifactValue(digest hash.Hash, value any) error {
	switch v := value.(type) {
	case nil:
		digest.Write([]byte{'0'})
	case bool:
		if v {
			digest.Write([]byte{'t'})
		} else {
			digest.Write([]byte{'f'})
		}
	case string:
		artifactText(digest, 's', v)
	case json.Number:
		normalized, err := canonicalArtifactNumber(string(v))
		if err != nil {
			return err
		}
		artifactText(digest, 'n', normalized)
	case []any:
		digest.Write([]byte{'a'})
		artifactLength(digest, len(v))
		for _, item := range v {
			if err := writeArtifactValue(digest, item); err != nil {
				return err
			}
		}
	case map[string]any:
		digest.Write([]byte{'o'})
		artifactLength(digest, len(v))
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			artifactText(digest, 'k', key)
			if err := writeArtifactValue(digest, v[key]); err != nil {
				return err
			}
		}
	default:
		return reject("unsupported artifact JSON value")
	}
	return nil
}

// Normalize decimal coefficient/exponent symbolically. Never compute 10^exp:
// a short malicious exponent must not cause an enormous big.Rat allocation.
func canonicalArtifactNumber(text string) (string, error) {
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign = "-"
		text = text[1:]
	}
	exponent := "0"
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		exponent = text[index+1:]
		text = text[:index]
	}
	fraction := 0
	if point := strings.IndexByte(text, '.'); point >= 0 {
		fraction = len(text) - point - 1
		text = text[:point] + text[point+1:]
	}
	digits := strings.TrimLeft(text, "0")
	if digits == "" {
		return "0", nil
	}
	coefficient := strings.TrimRight(digits, "0")
	trailing := len(digits) - len(coefficient)
	exp, ok := new(big.Int).SetString(exponent, 10)
	if !ok {
		return "", reject("invalid artifact exponent")
	}
	exp.Add(exp, big.NewInt(int64(trailing-fraction)))
	return sign + coefficient + "e" + exp.String(), nil
}
