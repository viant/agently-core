package finder

import "github.com/viant/agently-core/protocol/agent"

// Option mutates Finder during construction.
type Option func(*Finder)

func WithLoader(l agent.Loader) Option { return func(d *Finder) { d.loader = l } }

func WithInitial(agents ...*agent.Agent) Option {
	return func(d *Finder) {
		for _, a := range agents {
			if a == nil {
				continue
			}
			key := a.ID
			if key == "" {
				key = a.Name
			}
			d.Add(key, a)
		}
	}
}
