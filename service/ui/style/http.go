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
	mux.HandleFunc("GET /v1/workspace/ui/fonts/{asset}", s.serveFont)
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

func (s *Service) serveFont(w http.ResponseWriter, r *http.Request) {
	asset := r.PathValue("asset")
	digest := strings.TrimSuffix(asset, ".woff2")
	if !strings.HasSuffix(asset, ".woff2") || len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
		http.NotFound(w, r)
		return
	}
	lookup := func() []byte {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.selectRoot()
		for _, cached := range s.snapshots {
			if data := cached.fonts[asset]; len(data) > 0 {
				return data
			}
		}
		return nil
	}
	data := lookup()
	if len(data) == 0 {
		s.Current(r.Context())
		data = lookup()
	}
	if len(data) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "font/woff2")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("ETag", `"`+digest+`"`)
	http.ServeContent(w, r, asset, time.Time{}, bytes.NewReader(data))
}
