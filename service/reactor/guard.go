package reactor

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/viant/agently-core/protocol/agent/plan"
)

// WarnOnDuplicateSteps scans a plan for duplicate tool steps (same name and canonicalised args).
func WarnOnDuplicateSteps(p *plan.Plan, warn func(msg string)) {
	if p == nil || len(p.Steps) == 0 || warn == nil {
		return
	}
	type key struct{ Name, Args string }
	seen := map[key]struct{}{}
	for _, st := range p.Steps {
		if strings.TrimSpace(st.Type) != "tool" {
			continue
		}
		k := key{Name: strings.TrimSpace(st.Name), Args: canonicalArgsForWarning(st.Args)}
		if _, ok := seen[k]; ok {
			warn(fmt.Sprintf("duplicate tool step detected: %s %s", k.Name, k.Args))
			continue
		}
		seen[k] = struct{}{}
	}
}

func canonicalArgsForWarning(args map[string]interface{}) string {
	if len(args) == 0 {
		return "{}"
	}
	var canonicalize func(v interface{}) interface{}
	canonicalize = func(v interface{}) interface{} {
		switch tv := v.(type) {
		case map[string]interface{}:
			if len(tv) == 0 {
				return map[string]interface{}{}
			}
			keys := make([]string, 0, len(tv))
			for k := range tv {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			m := make(map[string]interface{}, len(tv))
			for _, k := range keys {
				m[k] = canonicalize(tv[k])
			}
			return m
		case []interface{}:
			out := make([]interface{}, 0, len(tv))
			for _, it := range tv {
				out = append(out, canonicalize(it))
			}
			return out
		default:
			return tv
		}
	}
	data, err := json.Marshal(canonicalize(args))
	if err != nil {
		return "{}"
	}
	return string(data)
}
