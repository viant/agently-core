package resource

import (
	"encoding/json"
	identity "github.com/viant/agently-core/protocol/resource"
	proto "github.com/viant/agently-core/protocol/skill"
	"path"
	"strings"
	"unicode/utf8"
)

// NativeSkillDefinition is the host's local primitive carrier. It preserves
// exact SKILL.md/supporting bytes; it is not the standard MCP skills manifest.
type NativeSkillDefinition struct {
	Document []byte            `json:"document"`
	Files    map[string][]byte `json:"files,omitempty"`
}

func ValidateNativeSkillDefinition(version int64, raw json.RawMessage) error {
	var value NativeSkillDefinition
	if version != 1 || json.Unmarshal(raw, &value) != nil || len(value.Document) == 0 || !utf8.Valid(value.Document) || len(value.Files) > 511 {
		return identity.ErrResource
	}
	item, diags, err := proto.Parse("SKILL.md", "", "workspace", string(value.Document))
	if err != nil || item == nil {
		return identity.ErrResource
	}
	for _, diag := range diags {
		if diag.Level == "error" {
			return identity.ErrResource
		}
	}
	total := len(value.Document)
	for name, data := range value.Files {
		if name == "SKILL.md" || name == "." || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "%\\:?#") {
			return identity.ErrResource
		}
		total += len(data)
	}
	if total > 16<<20 {
		return identity.ErrResource
	}
	return nil
}
