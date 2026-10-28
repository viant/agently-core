package skill

import (
	agentmdl "github.com/viant/agently-core/protocol/agent"
	proto "github.com/viant/agently-core/protocol/skill"
)

func configuredSkillRetention(agent *agentmdl.Agent, metadata proto.Metadata) bool {
	if agent == nil || len(agent.SkillsAlwaysInclude) == 0 {
		return false
	}
	return visibleRemoteResource(&agentmdl.Agent{Skills: agent.SkillsAlwaysInclude}, proto.Ref{ServerID: metadata.ServerID, Name: metadata.Name}, metadata.URI)
}
