package agent

import (
	"context"
	"encoding/json"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	skillproto "github.com/viant/agently-core/protocol/skill"
	agruntime "github.com/viant/agently-core/runtime"
	skillsvc "github.com/viant/agently-core/service/skill"
)

type activeInlineSkillState struct {
	Names   []string
	Skills  []*skillproto.Skill
	Primary *skillproto.Skill
}

func skillActivationModeOverride(input *QueryInput, skillName string) string {
	if input == nil || input.Runtime == nil || input.Runtime.SkillActivation == nil {
		return ""
	}
	value := input.Runtime.SkillActivation
	if !strings.EqualFold(strings.TrimSpace(value.Name), strings.TrimSpace(skillName)) {
		return ""
	}
	return strings.TrimSpace(value.Mode)
}

func skillActivationOverridePair(input *QueryInput) (string, string) {
	if input == nil || input.Runtime == nil || input.Runtime.SkillActivation == nil {
		return "", ""
	}
	return strings.TrimSpace(input.Runtime.SkillActivation.Name), strings.TrimSpace(input.Runtime.SkillActivation.Mode)
}

func runtimeActivatedSkill(input *QueryInput) (string, string, bool) {
	if input == nil || input.Runtime == nil || input.Runtime.SkillActivation == nil {
		return "", "", false
	}
	value := input.Runtime.SkillActivation
	if strings.TrimSpace(value.Name) == "" || strings.TrimSpace(value.Body) == "" {
		return "", "", false
	}
	return strings.TrimSpace(value.Name), strings.TrimSpace(value.Body), true
}

func runtimeActivatedSkillEmbedded(input *QueryInput) bool {
	if input == nil || input.Runtime == nil || input.Runtime.SkillActivation == nil {
		return false
	}
	return input.Runtime.SkillActivation.Embedded
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

func resolveActiveInlineSkillState(history *binding.History, input *QueryInput, svc *skillsvc.Service, agent *agentmdl.Agent) activeInlineSkillState {
	if svc == nil || agent == nil {
		return activeInlineSkillState{
			Names: resolveActiveSkillNames(history, input, svc, agent, "", ""),
		}
	}
	overrideName, overrideMode := skillActivationOverridePair(input)
	names := resolveActiveSkillNames(history, input, svc, agent, overrideName, overrideMode)
	if len(names) == 0 {
		return activeInlineSkillState{}
	}
	skills := svc.VisibleSkillsByName(agent, names)
	state := activeInlineSkillState{
		Names:  names,
		Skills: skills,
	}
	if len(skills) > 0 {
		state.Primary = skills[0]
	}
	return state
}

func preactivatedSkillPayload(input *QueryInput) (string, string, string, bool) {
	if input == nil || input.Runtime == nil || input.Runtime.SkillActivation == nil {
		return "", "", "", false
	}
	value := input.Runtime.SkillActivation
	if strings.TrimSpace(value.Name) == "" || strings.TrimSpace(value.Body) == "" {
		return "", "", "", false
	}
	return strings.TrimSpace(value.Name), strings.TrimSpace(value.Args), strings.TrimSpace(value.Body), true
}

type persistedSkillActivation struct {
	Name string `json:"name,omitempty"`
	Body string `json:"body,omitempty"`
	Mode string `json:"mode,omitempty"`
	Args string `json:"args,omitempty"`
}

func latestInlineSkillContextForTurn(conv *apiconv.Conversation, turnID string) *agruntime.Context {
	if conv == nil || strings.TrimSpace(turnID) == "" {
		return nil
	}
	for _, turn := range conv.GetTranscript() {
		if turn == nil || strings.TrimSpace(turn.Id) != strings.TrimSpace(turnID) {
			continue
		}
		for i := len(turn.Message) - 1; i >= 0; i-- {
			msg := turn.Message[i]
			if msg == nil {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(mcpname.Canonical(stringOrEmpty(msg.ToolName))), skillproto.ActivateToolNameCanonical) {
				continue
			}
			payload := parsePersistedSkillActivation(stringOrEmpty(msg.Content))
			if payload.Name == "" || payload.Body == "" {
				continue
			}
			mode := strings.TrimSpace(payload.Mode)
			if mode == "" {
				mode = "inline"
			}
			if !strings.EqualFold(mode, "inline") {
				continue
			}
			return &agruntime.Context{
				SkillActivation: &skillproto.ActivationContext{
					Name: payload.Name,
					Body: payload.Body,
					Mode: mode,
					Args: payload.Args,
				},
			}
		}
		break
	}
	return nil
}

func loadInlineSkillContextForTurn(ctx context.Context, client apiconv.Client, conversationID, turnID string) *agruntime.Context {
	if client == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(turnID) == "" {
		return nil
	}
	conv, err := client.GetConversation(ctx, strings.TrimSpace(conversationID), apiconv.WithIncludeTranscript(true))
	if err != nil || conv == nil {
		return nil
	}
	return latestInlineSkillContextForTurn(conv, turnID)
}

func parsePersistedSkillActivation(content string) persistedSkillActivation {
	var payload persistedSkillActivation
	if strings.TrimSpace(content) == "" {
		return payload
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return persistedSkillActivation{}
	}
	payload.Name = strings.TrimSpace(payload.Name)
	payload.Body = strings.TrimSpace(payload.Body)
	payload.Mode = strings.TrimSpace(payload.Mode)
	payload.Args = strings.TrimSpace(payload.Args)
	return payload
}
