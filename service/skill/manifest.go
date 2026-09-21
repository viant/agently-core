package skill

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/viant/mcp-protocol/schema"
	"sort"
	"strings"
)

func skillStateKey(name string) string {
	name = strings.TrimSpace(name)
	if strings.Contains(name, "/") {
		return name
	}
	return strings.ToLower(name)
}

func manifestRevision(entry schema.Skill) string {
	copy := entry
	copy.Resources.Files = append([]schema.SkillResource(nil), entry.Resources.Files...)
	sort.Slice(copy.Resources.Files, func(i, j int) bool { return copy.Resources.Files[i].Uri < copy.Resources.Files[j].Uri })
	data, _ := json.Marshal(copy)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
