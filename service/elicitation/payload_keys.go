package elicitation

import "sort"

func PayloadKeys(payload map[string]interface{}) []string {
	if len(payload) == 0 {
		return nil
	}
	out := make([]string, 0, len(payload))
	for k := range payload {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
