package skill

import (
	"sort"
	"strings"
)

type Registry struct {
	byName       map[string]*Skill
	diagnostics  []Diagnostic
	shadowedBy   map[string][]*Skill
	orderedNames []string
}

func NewRegistry() *Registry {
	return &Registry{
		byName:     map[string]*Skill{},
		shadowedBy: map[string][]*Skill{},
	}
}

func (r *Registry) Add(s *Skill, diags []Diagnostic) {
	if r == nil {
		return
	}
	r.diagnostics = append(r.diagnostics, diags...)
	if s == nil {
		return
	}
	name := strings.TrimSpace(s.Frontmatter.Name)
	if name == "" {
		return
	}
	if existing, ok := r.byName[name]; ok && existing != nil {
		r.shadowedBy[name] = append(r.shadowedBy[name], s)
		r.diagnostics = append(r.diagnostics, Diagnostic{
			Level:   "warning",
			Message: `skill "` + name + `" loaded from ` + existing.Root + `; shadowing ` + s.Root,
			Path:    s.Path,
		})
		return
	}
	r.byName[name] = s
	r.orderedNames = append(r.orderedNames, name)
	sort.Strings(r.orderedNames)
}

func (r *Registry) Get(name string) (*Skill, bool) {
	if r == nil {
		return nil, false
	}
	s, ok := r.byName[strings.TrimSpace(name)]
	return s, ok
}

func (r *Registry) List() []*Skill {
	if r == nil {
		return nil
	}
	out := make([]*Skill, 0, len(r.orderedNames))
	for _, name := range r.orderedNames {
		if s, ok := r.byName[name]; ok && s != nil {
			out = append(out, s)
		}
	}
	return out
}

func (r *Registry) Diagnostics() []Diagnostic {
	if r == nil {
		return nil
	}
	out := make([]Diagnostic, len(r.diagnostics))
	copy(out, r.diagnostics)
	return out
}
