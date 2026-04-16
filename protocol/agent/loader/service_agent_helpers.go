package loader

import (
	"fmt"
	"path/filepath"
	"strings"

	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	yml "github.com/viant/agently-core/workspace/service/meta/yml"
	"gopkg.in/yaml.v3"
)

// normalizeAgent applies generic cleanups that make downstream behavior stable.
func normalizeAgent(a *agentmdl.Agent) {
	trim := func(p *binding.Prompt) {
		if p == nil {
			return
		}
		if strings.TrimSpace(p.Text) != "" {
			p.Text = strings.TrimRight(p.Text, "\r\n\t ")
		}
	}
	trim(a.Prompt)
	trim(a.SystemPrompt)
	trim(a.InstructionPrompt)
	trim(a.Instruction)
	if a.InstructionPrompt == nil && a.Instruction != nil {
		a.InstructionPrompt = a.Instruction
	}
	for _, c := range a.EffectiveFollowUps() {
		if c == nil {
			continue
		}
		trim(c.Query)
		if c.When != nil {
			trim(c.When.Query)
		}
	}
}

// parseAgentBasicScalar handles common scalar fields and returns handled=true when processed.
func (s *Service) parseAgentBasicScalar(agent *agentmdl.Agent, key string, valueNode *yml.Node) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "id":
		if valueNode.Kind == yaml.ScalarNode {
			agent.ID = valueNode.Value
		}
		return true, nil
	case "name":
		if valueNode.Kind == yaml.ScalarNode {
			agent.Name = valueNode.Value
		}
		return true, nil
	case "icon":
		if valueNode.Kind == yaml.ScalarNode {
			agent.Icon = valueNode.Value
		}
		return true, nil
	case "modelref", "model":
		if valueNode.Kind == yaml.ScalarNode {
			agent.Model = strings.TrimSpace(valueNode.Value)
		}
		return true, nil
	case "temperature":
		if valueNode.Kind == yaml.ScalarNode {
			value := valueNode.Interface()
			temp := 0.0
			switch actual := value.(type) {
			case int:
				temp = float64(actual)
			case float64:
				temp = actual
			default:
				return true, fmt.Errorf("invalid temperature value: %T %v", value, value)
			}
			agent.Temperature = temp
		}
		return true, nil
	case "description":
		if valueNode.Kind == yaml.ScalarNode {
			agent.Description = valueNode.Value
		}
		return true, nil
	case "paralleltoolcalls":
		if valueNode.Kind == yaml.ScalarNode {
			val := valueNode.Interface()
			switch actual := val.(type) {
			case bool:
				boolVal := actual
				agent.ParallelToolCalls = &boolVal
			case string:
				lv := strings.ToLower(strings.TrimSpace(actual))
				boolVal2 := lv == "true"
				agent.ParallelToolCalls = &boolVal2
			}
		}
		return true, nil
	case "autosummarize", "autosumarize":
		if valueNode.Kind == yaml.ScalarNode {
			val := valueNode.Interface()
			switch actual := val.(type) {
			case bool:
				agent.AutoSummarize = &actual
			case string:
				lv := strings.ToLower(strings.TrimSpace(actual))
				v := lv == "true" || lv == "yes" || lv == "on"
				agent.AutoSummarize = &v
			}
		}
		return true, nil
	case "showexecutiondetails":
		if valueNode.Kind == yaml.ScalarNode {
			val := strings.ToLower(strings.TrimSpace(valueNode.Value))
			b := val == "true" || val == "yes" || val == "on" || val == "1"
			agent.ShowExecutionDetails = &b
		}
		return true, nil
	case "showtoolfeed":
		if valueNode.Kind == yaml.ScalarNode {
			val := strings.ToLower(strings.TrimSpace(valueNode.Value))
			b := val == "true" || val == "yes" || val == "on" || val == "1"
			agent.ShowToolFeed = &b
		}
		return true, nil
	}
	return false, nil
}

// getAgentNameFromURL extracts agent name from URL (file name without extension)
func getAgentNameFromURL(URL string) string {
	base := filepath.Base(URL)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
