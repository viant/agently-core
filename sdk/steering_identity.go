package sdk

import (
	"encoding/base64"
	"strings"
)

// Native steering keeps its canonical generated message ID. A reserved scalar
// tag in the existing message Tags column persists only the optimistic request
// correlation; it grants no ownership or host authority and introduces no table.
const steeringRequestTagPrefix = "agently.client-request.v1:"

func steeringRequestTag(id string) string {
	return steeringRequestTagPrefix + base64.RawURLEncoding.EncodeToString([]byte(id))
}
func steeringClientRequestID(tags *string) string {
	if tags == nil {
		return ""
	}
	for _, tag := range strings.Split(*tags, ",") {
		tag = strings.TrimSpace(tag)
		if !strings.HasPrefix(tag, steeringRequestTagPrefix) {
			continue
		}
		value, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(tag, steeringRequestTagPrefix))
		if err == nil && len(value) > 0 && len(value) <= 256 {
			return string(value)
		}
	}
	return ""
}
