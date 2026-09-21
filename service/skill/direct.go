package skill

import (
	"context"
	"fmt"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	proto "github.com/viant/agently-core/protocol/skill"
	"net/url"
	"path"
	"strings"
)

func normalizeSkillInput(name, uri, server string) (string, error) {
	if uri == "" {
		if server != "" {
			return "", fmt.Errorf("server requires URI")
		}
		if strings.TrimSpace(name) == "" {
			return "", fmt.Errorf("skill name or URI required")
		}
		return name, nil
	}
	if name != "" && name != uri {
		return "", fmt.Errorf("specify name or URI")
	}
	if server == "" {
		return uri, nil
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme == "" || !strings.HasSuffix(u.Path, "/SKILL.md") || !proto.ValidCatalogSegment(server) {
		return "", fmt.Errorf("invalid skill reference")
	}
	n := path.Base(strings.TrimSuffix(u.Path, "/SKILL.md"))
	if n == "." || n == "/" {
		n = u.Host
	}
	return (proto.Ref{ServerID: server, Name: n, ResourceURI: uri}).URI(), nil
}

// A direct reference is confirmed by skills/get, including skills omitted by
// a partial list. Caller input never chooses an endpoint or bypasses visibility.
func (s *Service) getDirect(ctx context.Context, agent *agentmdl.Agent, ref proto.Ref) (*proto.Skill, error) {
	if s.mcpSource == nil || authctx.EffectiveUserID(ctx) == "" || !visibleRemote(agent, ref) {
		return nil, fmt.Errorf("skill not available")
	}
	names, err := s.mcpSource.Names(ctx)
	if err != nil {
		return nil, err
	}
	var selected *remoteEntry
	for _, name := range names {
		cfg, err := s.mcpSource.Options(ctx, name)
		if err != nil {
			return nil, err
		}
		if cfg == nil || cfg.SkillDiscovery == nil || !cfg.SkillDiscovery.Enabled {
			continue
		}
		id := cfg.SkillDiscovery.CatalogID
		if id == "" {
			id = name
		}
		if id != ref.ServerID {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("duplicate catalog ID")
		}
		selected = &remoteEntry{revision: ref.Revision, server: name, uri: ref.ResourceURI, config: cfg, metadata: proto.Metadata{Name: ref.Name, URI: ref.URI()}}
	}
	if selected == nil {
		return nil, fmt.Errorf("skill not available")
	}
	return s.readRemote(ctx, *selected)
}
