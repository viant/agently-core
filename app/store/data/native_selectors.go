package data

import (
	"github.com/viant/xdatly/state"
)

// nativeSelectors preserves caller-owned projections, ordering and pagination
// when forwarding them to canonical v1 readers.
func nativeSelectors(items []*state.NamedSelector) state.Selectors {
	result := make(state.Selectors, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		result = append(result, &state.NamedSelector{Name: item.Name, Selector: state.Selector{
			Columns: append([]string(nil), item.Columns...),
			Fields:  append([]string(nil), item.Fields...),
			OrderBy: item.OrderBy, Offset: item.Offset, Limit: item.Limit, Page: item.Page,
			Criteria: item.Criteria, Placeholders: append([]interface{}(nil), item.Placeholders...),
		}})
	}
	return result
}
