package agent

import (
	"strings"

	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	skillsvc "github.com/viant/agently-core/service/skill"
)

func runtimeActivatedSkill(input *QueryInput) (string, string, bool) {
	if input == nil || input.Context == nil {
		return "", "", false
	}
	name, _ := input.Context["skillActivationName"].(string)
	body, _ := input.Context["skillActivationBody"].(string)
	name = strings.TrimSpace(name)
	body = strings.TrimSpace(body)
	if name == "" || body == "" {
		return "", "", false
	}
	return name, body, true
}

func runtimeActivatedSkillEmbedded(input *QueryInput) bool {
	if input == nil || input.Context == nil {
		return false
	}
	value, ok := input.Context["skillActivationEmbedded"]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func resolveActiveSkillNames(history *binding.History, input *QueryInput, svc *skillsvc.Service, agent *agentmdl.Agent, overrideName, overrideMode string) []string {
	names := skillsvc.InlineActiveSkillsFromHistory(history, svc, agent, overrideName, overrideMode)
	if len(names) > 0 {
		return names
	}
	name, _, ok := runtimeActivatedSkill(input)
	if !ok {
		return nil
	}
	return []string{name}
}
