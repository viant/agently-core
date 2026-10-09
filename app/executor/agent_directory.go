package executor

import (
	agentmodel "github.com/viant/agently-core/protocol/agent"
	llmagents "github.com/viant/agently-core/protocol/tool/service/llm/agents"
	"sort"
	"strings"
)

func localAgentDirectory(finder agentmodel.Finder, external func() []llmagents.ListItem) func() []llmagents.ListItem {
	return func() []llmagents.ListItem {
		entries := map[string]llmagents.ListItem{}
		if catalog, ok := finder.(interface{ All() []*agentmodel.Agent }); ok {
			for _, a := range catalog.All() {
				if a == nil {
					continue
				}
				id := strings.TrimSpace(a.ID)
				if id == "" {
					id = strings.TrimSpace(a.Name)
				}
				if id == "" {
					continue
				}
				item := llmagents.ListItem{ID: id, Name: a.Name, Description: a.Description, Internal: a.Internal, Source: "internal"}
				if a.Profile != nil {
					item.Tags = append([]string(nil), a.Profile.Tags...)
					item.Priority = a.Profile.Rank
					if item.Description == "" {
						item.Description = a.Profile.Description
					}
				}
				entries[item.ID] = item
			}
		}
		if external != nil {
			for _, item := range external() {
				if strings.TrimSpace(item.ID) != "" {
					entries[item.ID] = item
				}
			}
		}
		result := make([]llmagents.ListItem, 0, len(entries))
		for _, item := range entries {
			result = append(result, item)
		}
		sort.SliceStable(result, func(i, j int) bool {
			if result[i].Priority != result[j].Priority {
				return result[i].Priority > result[j].Priority
			}
			if result[i].Name != result[j].Name {
				return result[i].Name < result[j].Name
			}
			return result[i].ID < result[j].ID
		})
		return result
	}
}
