package loader

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/viant/afs/file"
	"github.com/viant/afs/url"
	"github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/prompt"
	yml "github.com/viant/agently-core/workspace/service/meta/yml"
	"gopkg.in/yaml.v3"
)

// resolvePromptURIs updates agent Prompt/SystemPrompt/Instruction URI when relative by
// resolving them against the agent source URL directory.
func (s *Service) resolvePromptURIs(a *agent.Agent) {
	if a == nil || a.Source == nil || strings.TrimSpace(a.Source.URL) == "" {
		return
	}
	base, _ := url.Split(a.Source.URL, file.Scheme)
	resolvePath := func(p *prompt.Prompt) {
		if p == nil {
			return
		}
		u := strings.TrimSpace(p.URI)
		if u == "" {
			return
		}
		if url.Scheme(u, "") == "" && !strings.HasPrefix(u, "/") {
			p.URI = url.Join(base, u)
		}
	}
	resolvePath(a.Prompt)
	resolvePath(a.SystemPrompt)
	resolvePath(a.InstructionPrompt)
	resolvePath(a.Instruction)
	for _, chain := range a.EffectiveFollowUps() {
		if query := chain.Query; query != nil && query.URI != "" {
			resolvePath(query)
			if when := chain.When; when != nil && when.Query != nil && when.Query.URI != "" {
				resolvePath(when.Query)
			}
		}
	}
}

func (s *Service) getPrompt(valueNode *yml.Node) (*prompt.Prompt, error) {
	var aPrompt *prompt.Prompt
	if valueNode.Kind == yaml.ScalarNode {
		aPrompt = &prompt.Prompt{Text: valueNode.Value}
		inferPromptEngine(aPrompt)
	} else if valueNode.Kind == yaml.MappingNode {
		var err error
		if aPrompt, err = parsePrompt((*yml.Node)(valueNode)); err != nil {
			return nil, err
		}
	}
	return aPrompt, nil
}

func parsePrompt(y *yml.Node) (*prompt.Prompt, error) {
	if y == nil {
		return &prompt.Prompt{}, nil
	}
	if y.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("prompt node should be a mapping")
	}
	p := &prompt.Prompt{}
	if err := y.Pairs(func(key string, v *yml.Node) error {
		k := strings.ToLower(strings.TrimSpace(key))
		switch k {
		case "text", "content":
			if v.Kind == yaml.ScalarNode {
				p.Text = v.Value
			}
		case "uri", "url", "path", "file":
			if v.Kind == yaml.ScalarNode {
				p.URI = v.Value
			}
		case "engine", "type":
			if v.Kind == yaml.ScalarNode {
				p.Engine = strings.ToLower(strings.TrimSpace(v.Value))
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	inferPromptEngine(p)
	return p, nil
}

// inferPromptEngine sets prompt.Engine if empty using URI suffixes or inline text markers.
func inferPromptEngine(p *prompt.Prompt) {
	if p == nil || strings.TrimSpace(p.Engine) != "" {
		return
	}
	if u := strings.TrimSpace(p.URI); u != "" {
		cand := u
		if strings.HasPrefix(cand, "$path(") && strings.HasSuffix(cand, ")") {
			cand = strings.TrimSuffix(strings.TrimPrefix(cand, "$path("), ")")
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(cand), "."))
		switch ext {
		case "vm":
			p.Engine = "vm"
		case "gotmpl", "tmpl":
			p.Engine = "go"
		}
	}
	if strings.TrimSpace(p.Engine) == "" {
		if strings.Contains(p.Text, "{{") && strings.Contains(p.Text, "}}") {
			p.Engine = "go"
		} else if strings.Contains(p.Text, "$") {
			p.Engine = "vm"
		}
	}
}
