package tool

import "strings"

// RegisterMetadataConnections classifies trusted bootstrap transports. Cohort
// metadata is served by the local artifact source; executable connections come
// from its user-visible MCP definitions, not from probing the bootstrap server.
func (r *Registry) RegisterMetadataConnections(names ...string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.metadataOnly == nil {
		r.metadataOnly = map[string]bool{}
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || r.internal[name] != nil {
			continue
		}
		r.metadataOnly[name] = true
	}
	for key, value := range r.cache {
		if value != nil && r.metadataOnly[value.def.SourceMCPServer] {
			delete(r.cache, key)
		}
	}
}

func (r *Registry) metadataConnection(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.metadataOnly[strings.TrimSpace(name)]
}
