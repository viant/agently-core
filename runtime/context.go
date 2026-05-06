package runtime

import (
	"strings"

	skillproto "github.com/viant/agently-core/protocol/skill"
)

// Context carries turn-execution state that is not part of intake,
// routing, or ordinary user/task context.
type Context struct {
	SkillActivation *skillproto.ActivationContext `json:"skillActivation,omitempty"`
	ModelSource     string                        `json:"modelSource,omitempty"`
	Workdir         string                        `json:"workdir,omitempty"`
	ResolvedWorkdir string                        `json:"resolvedWorkdir,omitempty"`
	BearerToken     string                        `json:"bearerToken,omitempty"`
}

func (c *Context) EffectiveWorkdir() string {
	if c == nil {
		return ""
	}
	if v := strings.TrimSpace(c.ResolvedWorkdir); v != "" {
		return v
	}
	return strings.TrimSpace(c.Workdir)
}

func ProjectVisibleContext(target map[string]interface{}, rt *Context, includeModelSource bool) map[string]interface{} {
	if target == nil {
		target = map[string]interface{}{}
	}
	if rt == nil {
		return target
	}
	if includeModelSource {
		if source := strings.TrimSpace(rt.ModelSource); source != "" {
			if _, ok := target["modelSource"]; !ok {
				target["modelSource"] = source
			}
		}
	}
	if workdir := rt.EffectiveWorkdir(); workdir != "" {
		if _, ok := target["workdir"]; !ok {
			target["workdir"] = workdir
		}
		if _, ok := target["resolvedWorkdir"]; !ok {
			target["resolvedWorkdir"] = workdir
		}
	}
	return target
}
