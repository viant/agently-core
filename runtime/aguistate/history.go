package aguistate

import "encoding/json"

// replaceHistory follows the pinned client snapshot rules: ordinary history is
// authoritative; omitted client activity/reasoning survives unless declared owned.
func (p *Projection) replaceHistory(e Object) error {
	var incoming []Object
	data, err := json.Marshal(e["messages"])
	if err != nil {
		return err
	}
	if err = decode(data, &incoming); err != nil {
		return err
	}
	p.seedOwners(incoming, true)
	byID := map[string]Object{}
	hasActivity, hasReasoning := false, false
	for _, m := range incoming {
		byID[field(m, "id")] = m
		hasActivity = hasActivity || field(m, "role") == "activity"
		hasReasoning = hasReasoning || field(m, "role") == "reasoning"
	}
	// explicit distinguishes absent inference from an invalid or empty declaration.
	explicit, all := false, false
	owned := map[string]bool{}
	if metadata := object(e["metadata"]); metadata != nil {
		if raw, present := metadata["@ag-ui/client"]; present {
			scope := object(raw)
			if scope == nil {
				explicit = true
			} else if types, present := scope["authoritativeActivityTypes"]; present {
				explicit = true
				if types == nil {
					all = true
				} else if values, ok := types.([]any); ok {
					valid := true
					for _, value := range values {
						name, ok := value.(string)
						if !ok {
							valid = false
							break
						}
						owned[name] = true
					}
					if !valid {
						owned = map[string]bool{}
					}
				}
			}
		}
	}
	result := make([]Object, 0, len(p.Messages)+len(incoming))
	existing := map[string]bool{}
	for _, m := range p.Messages {
		id := field(m, "id")
		if replacement, known := byID[id]; known {
			result = append(result, replacement)
			existing[id] = true
			continue
		}
		preserve := field(m, "role") == "reasoning" && !hasReasoning
		if field(m, "role") == "activity" {
			preserve = explicit && !all && !owned[field(m, "activityType")] || !explicit && !hasActivity
		}
		if preserve {
			result = append(result, m)
			existing[id] = true
		}
	}
	for _, m := range incoming {
		if !existing[field(m, "id")] {
			result = append(result, m)
		}
	}
	p.Messages = result
	p.indexTools()
	return nil
}
