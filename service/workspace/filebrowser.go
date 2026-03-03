package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/viant/afs"
)

// FileBrowserEntry represents a file or directory in the file browser.
type FileBrowserEntry struct {
	Name    string `json:"name"`
	URI     string `json:"uri"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size,omitempty"`
	ModTime string `json:"modTime,omitempty"`
}

// FileBrowserHandler serves file browser endpoints using afs for
// scheme-agnostic file access.
type FileBrowserHandler struct {
	fs afs.Service
}

// NewFileBrowserHandler creates a file browser handler.
func NewFileBrowserHandler() *FileBrowserHandler {
	return &FileBrowserHandler{fs: afs.New()}
}

// Register mounts file browser routes on the given mux.
func (h *FileBrowserHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/workspace/file-browser/list", h.handleList())
	mux.HandleFunc("GET /v1/workspace/file-browser/download", h.handleDownload())
}

func (h *FileBrowserHandler) handleList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uri := r.URL.Query().Get("uri")
		if uri == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("uri parameter is required"))
			return
		}
		folderOnly := strings.EqualFold(r.URL.Query().Get("folderOnly"), "true")

		objects, err := h.fs.List(r.Context(), uri)
		if err != nil {
			httpError(w, http.StatusInternalServerError, fmt.Errorf("list %s: %w", uri, err))
			return
		}

		entries := make([]FileBrowserEntry, 0, len(objects))
		for _, obj := range objects {
			if folderOnly && !obj.IsDir() {
				continue
			}
			// Skip the parent directory entry.
			if obj.Name() == "" || obj.Name() == "." {
				continue
			}
			entry := FileBrowserEntry{
				Name:  obj.Name(),
				URI:   obj.URL(),
				IsDir: obj.IsDir(),
				Size:  obj.Size(),
			}
			if !obj.ModTime().IsZero() {
				entry.ModTime = obj.ModTime().Format("2006-01-02T15:04:05Z")
			}
			entries = append(entries, entry)
		}
		httpJSON(w, http.StatusOK, map[string]interface{}{"entries": entries})
	}
}

func (h *FileBrowserHandler) handleDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uri := r.URL.Query().Get("uri")
		if uri == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("uri parameter is required"))
			return
		}
		reader, err := h.fs.OpenURL(r.Context(), uri)
		if err != nil {
			httpError(w, http.StatusInternalServerError, fmt.Errorf("open %s: %w", uri, err))
			return
		}
		defer reader.Close()
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(uri)))
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.Copy(w, reader)
	}
}

func httpJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
