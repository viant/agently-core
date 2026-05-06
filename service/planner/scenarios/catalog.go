package scenarios

import (
	"sort"
	"strings"

	promptdef "github.com/viant/agently-core/protocol/prompt"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
)

func Catalog(profiles []*promptdef.Profile, allow []string) string {
	profiles = promptrepo.FilterAllowedProfiles(profiles, allow)
	if len(profiles) == 0 {
		return ""
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		if profiles[i] == nil || profiles[j] == nil {
			return false
		}
		return strings.ToLower(strings.TrimSpace(profiles[i].ID)) < strings.ToLower(strings.TrimSpace(profiles[j].ID))
	})
	lines := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		if profile == nil {
			continue
		}
		id := strings.TrimSpace(profile.ID)
		if id == "" {
			continue
		}
		desc := strings.TrimSpace(profile.Description)
		tags := strings.Join(profile.AppliesTo, ", ")
		switch {
		case desc != "" && tags != "":
			lines = append(lines, "- "+id+": "+desc+" ["+tags+"]")
		case desc != "":
			lines = append(lines, "- "+id+": "+desc)
		case tags != "":
			lines = append(lines, "- "+id+" ["+tags+"]")
		default:
			lines = append(lines, "- "+id)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "Available scenario priors:\n" + strings.Join(lines, "\n")
}
