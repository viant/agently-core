package overlay

import (
	"sort"

	loproto "github.com/viant/agently-core/protocol/lookup/overlay"
)

// Registry returns the set of named-token bindings available for the given
// render context. It is what GET /v1/api/lookups/registry returns.
//
// A named binding is any Binding whose Named field is non-nil. The matcher
// does NOT need to consult a schema for named bindings: they are activated
// either (b) by live /name typing in a text input, or (c) by authored /name
// tokens in a prompt body. Both flows are schema-agnostic.
//
// Composition: if two overlays declare the same Named.Name, the higher-
// priority overlay wins; ties fall back to overlay-ID ordering for
// determinism.
func (s *Service) Registry(contextKind, contextID string) []loproto.RegistryEntry {
	if s == nil || s.store == nil {
		return nil
	}

	type ranked struct {
		priority int
		overlay  string
		entry    loproto.RegistryEntry
	}
	byName := make(map[string]ranked)

	for _, ov := range s.store.List() {
		if ov == nil {
			continue
		}
		// For named bindings, only Target Kind/ID matter — schema is not
		// consulted. Bindings with neither Path/FieldName ("pure named"
		// bindings) are still valid here.
		if !targetMatches(ov.Target, contextKind, contextID, map[string]interface{}{}) {
			// Permit overlays whose Target matches kind-only (no id) even
			// when SchemaContains is set — ignore SchemaContains for the
			// registry path; we don't have the schema here.
			if !targetMatchesNamed(ov.Target, contextKind, contextID) {
				continue
			}
		}
		for _, b := range ov.Bindings {
			if b.Named == nil || b.Named.Name == "" {
				continue
			}
			entry := loproto.RegistryEntry{
				Name:       b.Named.Name,
				DataSource: b.Lookup.DataSource,
				Trigger:    triggerOrDefault(b.Named.Trigger),
				Required:   b.Named.Required,
				Display:    firstNonEmpty(b.Named.Display, b.Lookup.Display),
				Inputs:     b.Lookup.Inputs,
				Outputs:    b.Lookup.Outputs,
			}
			if b.Named.Store != "" || b.Named.Display != "" || b.Named.ModelForm != "" {
				entry.Token = &loproto.TokenFormat{
					Store:     b.Named.Store,
					Display:   b.Named.Display,
					ModelForm: b.Named.ModelForm,
				}
			}
			candidate := ranked{priority: ov.Priority, overlay: ov.ID, entry: entry}
			existing, ok := byName[b.Named.Name]
			if !ok || candidate.priority > existing.priority ||
				(candidate.priority == existing.priority && candidate.overlay < existing.overlay) {
				byName[b.Named.Name] = candidate
			}
		}
	}

	out := make([]loproto.RegistryEntry, 0, len(byName))
	for _, r := range byName {
		out = append(out, r.entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// targetMatchesNamed is a loosened matcher for the registry path: it ignores
// SchemaContains (we don't have a schema) but still honours Kind/ID/IDGlob.
func targetMatchesNamed(t loproto.Target, contextKind, contextID string) bool {
	if t.Kind != "" && t.Kind != contextKind {
		return false
	}
	if t.ID != "" && t.ID != contextID {
		return false
	}
	if t.IDGlob != "" {
		re, err := globToRegex(t.IDGlob)
		if err != nil || !re.MatchString(contextID) {
			return false
		}
	}
	return true
}

func triggerOrDefault(t string) string {
	if t == "" {
		return "/"
	}
	return t
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
