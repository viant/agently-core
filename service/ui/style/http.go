package style

import (
	"bytes"
	"net/http"
	"strings"
	"time"
)

// Register attaches routes to the same mux protected by the host's workspace
// authentication middleware. It does not install an independent public server.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/workspace/ui/styles/{asset}", func(w http.ResponseWriter, r *http.Request) { s.serve(w, r, ".css") })
	mux.HandleFunc("GET /v1/workspace/ui/themes/{asset}", func(w http.ResponseWriter, r *http.Request) { s.serve(w, r, ".json") })
}
func (s *Service) serve(w http.ResponseWriter, r *http.Request, extension string) {
	asset := r.PathValue("asset")
	revision := strings.TrimSuffix(asset, extension)
	if !strings.HasSuffix(asset, extension) || len(revision) != 64 || strings.Trim(revision, "0123456789abcdef") != "" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	s.selectRoot()
	cached := s.snapshots[revision]
	s.mu.Unlock()
	if cached == nil {
		// On restart, reconstruct the current revision only. Never serve current
		// bytes for a request naming a different immutable revision.
		s.Current(r.Context())
		s.mu.Lock()
		s.selectRoot()
		cached = s.snapshots[revision]
		s.mu.Unlock()
	}
	if cached == nil {
		http.NotFound(w, r)
		return
	}
	data := cached.css
	contentType := "text/css; charset=utf-8"
	if extension == ".json" {
		data = cached.catalog
		contentType = "application/json; charset=utf-8"
		if len(data) == 0 {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", `"`+revision+extension+`"`)
	http.ServeContent(w, r, asset, time.Time{}, bytes.NewReader(data))
}
