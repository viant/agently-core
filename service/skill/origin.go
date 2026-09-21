package skill

import proto "github.com/viant/agently-core/protocol/skill"

func (s *RuntimeState) allowsSkillRead(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	target, err := proto.ParseRef(name)
	remote := false
	matched := false
	for active := range s.active {
		ref, e := proto.ParseRef(active)
		if e != nil {
			continue
		}
		remote = true
		if err == nil && ref.ServerID == target.ServerID {
			matched = true
		}
	}
	return !remote || matched
}
