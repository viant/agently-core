package skill

import (
	"context"
	"fmt"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	proto "github.com/viant/agently-core/protocol/skill"
)

// Delivery is installed by host composition before serving. The host adapter
// uses the one primitive gateway; the skill service retains selection and local
// execution semantics without depending on the gateway implementation package.
type Delivery interface {
	ReadLocal(context.Context, *proto.Skill) (*proto.Skill, error)
	List(context.Context) ([]proto.Metadata, error)
	Read(context.Context, string) (*proto.Skill, bool, error)
}

func (s *Service) SetDelivery(source MCPSource, delivery Delivery) {
	s.mcpSource = source
	s.delivery = delivery
}
func (s *Service) LocalSnapshot() []*proto.Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.registry == nil {
		return nil
	}
	var out []*proto.Skill
	for _, item := range s.registry.List() {
		if item != nil {
			copy := *item
			out = append(out, &copy)
		}
	}
	return out
}
func (s *Service) getNativeLocal(ctx context.Context, item *proto.Skill) (*proto.Skill, error) {
	if s.delivery == nil {
		return item, nil
	}
	return s.delivery.ReadLocal(ctx, item)
}
func (s *Service) primitiveMetadata(ctx context.Context, agent *agentmdl.Agent) ([]proto.Metadata, error) {
	if s.delivery == nil || agent == nil || len(agent.Skills) == 0 {
		return nil, nil
	}
	rows, err := s.delivery.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []proto.Metadata
	for _, row := range rows {
		if visibleRemote(agent, proto.Ref{ServerID: row.ServerID, Name: row.Name}) {
			out = append(out, row)
		}
	}
	return out, nil
}
func (s *Service) getPrimitive(ctx context.Context, agent *agentmdl.Agent, key string) (*proto.Skill, bool, error) {
	if s.delivery == nil {
		return nil, false, nil
	}
	rows, err := s.primitiveMetadata(ctx, agent)
	if err != nil {
		return nil, true, err
	}
	matched := false
	for _, row := range rows {
		if row.URI == key {
			matched = true
			break
		}
	}
	if !matched {
		return nil, false, nil
	}
	item, handled, err := s.delivery.Read(ctx, key)
	if err != nil || !handled {
		return nil, handled, err
	}
	if !visibleRemote(agent, proto.Ref{ServerID: item.ServerID, Name: item.Frontmatter.Name}) {
		return nil, true, fmt.Errorf("skill not available")
	}
	if item.Source != "mcp" {
		return item, true, nil
	}
	item, err = s.resolveRemoteTools(ctx, item)
	return item, true, err
}

// OwnsNativePath recognizes only a trusted startup-local skill entrypoint.
// A denied owning Get must not fall back to raw filesystem delivery.
func (s *Service) OwnsNativePath(path string) bool {
	if s == nil || s.delivery == nil {
		return false
	}
	target := normalizeSkillPath(path)
	for _, item := range s.LocalSnapshot() {
		if normalizeSkillPath(item.Path) == target {
			return true
		}
	}
	return false
}

// ReadNativeFile lets the existing raw resource reader retain its exact-byte
// contract while taking owned skill files through the same fresh delivery.
func (s *Service) ReadNativeFile(ctx context.Context, path string) ([]byte, bool, error) {
	if s == nil || s.delivery == nil {
		return nil, false, nil
	}
	if native, ok := s.delivery.(interface {
		ReadNativeFile(context.Context, string) ([]byte, bool, error)
	}); ok {
		return native.ReadNativeFile(ctx, path)
	}
	return nil, false, nil
}
