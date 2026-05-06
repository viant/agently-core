package oauth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

func parseJWTExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return time.Time{}, false
	}
	expAny, ok := decoded["exp"]
	if !ok {
		return time.Time{}, false
	}
	switch v := expAny.(type) {
	case float64:
		return time.Unix(int64(v), 0), true
	case int64:
		return time.Unix(v, 0), true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return time.Unix(n, 0), true
		}
	}
	return time.Time{}, false
}
