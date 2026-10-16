package overlay

import lookup "github.com/viant/agently-core/protocol/lookup/overlay"

type capturedDefinitions []*lookup.Overlay

func (s capturedDefinitions) List() []*lookup.Overlay { return s }

// Combine keeps first-class definitions authoritative while retaining legacy
// owner attachments. Both use the same existing matching/priority engine.
func Combine(primary, fallback *Service) *Service {
	var definitions []*lookup.Overlay
	seen := map[string]bool{}
	for _, service := range []*Service{primary, fallback} {
		if service == nil || service.store == nil {
			continue
		}
		for _, definition := range service.store.List() {
			if definition == nil || seen[definition.ID] {
				continue
			}
			seen[definition.ID] = true
			definitions = append(definitions, definition)
		}
	}
	return New(capturedDefinitions(definitions))
}
