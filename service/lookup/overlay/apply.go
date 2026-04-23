package overlay

import (
	"sort"
	"sync"

	loproto "github.com/viant/agently-core/protocol/lookup/overlay"
)

// Store is the workspace-scoped registry of loaded overlays.
type Store interface {
	List() []*loproto.Overlay
}

// Service is the public entry point. Construct with New, then call Apply and
// Registry.
type Service struct {
	store Store
}

func New(store Store) *Service { return &Service{store: store} }

// Apply walks matching overlays for the given render context, evaluates each
// overlay's mode, composes the surviving bindings (priority-descending,
// deterministic tie-break by overlay ID), and attaches forge Item.Lookup
// metadata onto the schema's properties.
//
// schemaProps is mutated in place: matched properties gain x-ui-widget and
// x-ui-lookup metadata. Returns the set of applied bindings (useful for
// debugging and for the registry endpoint when it needs per-property
// information).
func (s *Service) Apply(contextKind, contextID string, schemaProps map[string]interface{}) []AppliedBinding {
	if s == nil || s.store == nil || schemaProps == nil {
		return nil
	}

	survivors := s.evaluateOverlays(contextKind, contextID, schemaProps)
	// Sort survivors: higher priority first; deterministic tie-break by
	// overlay ID ascending, then by property name ascending.
	sort.SliceStable(survivors, func(i, j int) bool {
		if survivors[i].Priority != survivors[j].Priority {
			return survivors[i].Priority > survivors[j].Priority
		}
		if survivors[i].OverlayID != survivors[j].OverlayID {
			return survivors[i].OverlayID < survivors[j].OverlayID
		}
		return survivors[i].Property < survivors[j].Property
	})

	seen := make(map[string]struct{})
	var applied []AppliedBinding
	for _, sb := range survivors {
		if _, dup := seen[sb.Property]; dup {
			continue // first wins after priority-sort
		}
		seen[sb.Property] = struct{}{}
		attachLookup(schemaProps, sb.Property, sb.Lookup)
		applied = append(applied, sb)
	}
	return applied
}

// AppliedBinding records one (property → lookup) attachment that survived
// matching and composition.
type AppliedBinding struct {
	OverlayID string
	Priority  int
	Property  string
	Lookup    loproto.Lookup
	// Named carries the hotkey/authored-text configuration when this
	// binding also participates in named-token activation.
	Named *loproto.NamedToken
}

// evaluateOverlays runs each matching overlay through its mode and returns
// all survivors (possibly multiple overlays can emit bindings for the same
// property; conflict resolution happens in Apply).
func (s *Service) evaluateOverlays(contextKind, contextID string, schemaProps map[string]interface{}) []AppliedBinding {
	var out []AppliedBinding
	for _, ov := range s.store.List() {
		if ov == nil {
			continue
		}
		if !targetMatches(ov.Target, contextKind, contextID, schemaProps) {
			continue
		}

		type matchedBinding struct {
			binding loproto.Binding
			hits    []string
		}
		perBinding := make([]matchedBinding, 0, len(ov.Bindings))
		matchedCount := 0
		for _, b := range ov.Bindings {
			hits := matchBinding(schemaProps, b.Match)
			if len(hits) > 0 {
				matchedCount++
			}
			perBinding = append(perBinding, matchedBinding{binding: b, hits: hits})
		}

		mode := ov.Mode
		if mode == "" {
			mode = loproto.ModePartial
		}
		switch mode {
		case loproto.ModeStrict:
			if matchedCount != len(ov.Bindings) {
				continue // discard whole overlay
			}
		case loproto.ModeThreshold:
			th := ov.Threshold
			if th < 1 {
				th = 1
			}
			if matchedCount < th {
				continue
			}
		case loproto.ModePartial:
			// fall through — keep whichever bindings matched.
		}

		for _, mb := range perBinding {
			for _, prop := range mb.hits {
				out = append(out, AppliedBinding{
					OverlayID: ov.ID,
					Priority:  ov.Priority,
					Property:  prop,
					Lookup:    mb.binding.Lookup,
					Named:     mb.binding.Named,
				})
			}
		}
	}
	return out
}

// attachLookup writes forge-compatible Item.Lookup metadata onto a schema
// property. The client renderer consumes this via existing x-ui-* channels.
func attachLookup(schemaProps map[string]interface{}, propName string, lk loproto.Lookup) {
	prop, ok := schemaProps[propName].(map[string]interface{})
	if !ok {
		return
	}
	prop["x-ui-widget"] = "lookup"
	attachment := map[string]interface{}{}
	if lk.DataSource != "" {
		attachment["dataSource"] = lk.DataSource
	}
	if lk.DialogId != "" {
		attachment["dialogId"] = lk.DialogId
	}
	if lk.WindowId != "" {
		attachment["windowId"] = lk.WindowId
	}
	if lk.Display != "" {
		attachment["display"] = lk.Display
	}
	if len(lk.Inputs) > 0 {
		attachment["inputs"] = encodeParams(lk.Inputs)
	}
	if len(lk.Outputs) > 0 {
		attachment["outputs"] = encodeParams(lk.Outputs)
	}
	prop["x-ui-lookup"] = attachment
	schemaProps[propName] = prop
}

func encodeParams(ps []loproto.Parameter) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(ps))
	for _, p := range ps {
		m := map[string]interface{}{"name": p.Name}
		if p.From != "" {
			m["from"] = p.From
		}
		if p.To != "" {
			m["to"] = p.To
		}
		if p.Location != "" {
			m["location"] = p.Location
		}
		out = append(out, m)
	}
	return out
}

// MemoryStore is a simple Store implementation suitable for production
// (loaded from workspace repository) and tests.
type MemoryStore struct {
	mu sync.RWMutex
	m  []*loproto.Overlay
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

// Replace swaps the overlay set atomically.
func (s *MemoryStore) Replace(items []*loproto.Overlay) {
	cp := make([]*loproto.Overlay, len(items))
	copy(cp, items)
	s.mu.Lock()
	s.m = cp
	s.mu.Unlock()
}

// List returns a snapshot.
func (s *MemoryStore) List() []*loproto.Overlay {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*loproto.Overlay, len(s.m))
	copy(out, s.m)
	return out
}
