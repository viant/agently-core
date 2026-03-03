package speech

import (
	"encoding/json"
	"fmt"
	"net/http"
)

const defaultMaxUploadSize = 25 << 20 // 25 MB

// Handler serves speech transcription endpoints.
type Handler struct {
	transcriber   Transcriber
	maxUploadSize int64
}

// NewHandler creates a speech handler with the given transcriber.
func NewHandler(t Transcriber, opts ...HandlerOption) *Handler {
	h := &Handler{
		transcriber:   t,
		maxUploadSize: defaultMaxUploadSize,
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

// HandlerOption customises the speech handler.
type HandlerOption func(*Handler)

// WithMaxUploadSize overrides the maximum upload size in bytes.
func WithMaxUploadSize(n int64) HandlerOption {
	return func(h *Handler) { h.maxUploadSize = n }
}

// Register mounts speech routes on the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/api/speech/transcribe", h.handleTranscribe())
}

func (h *Handler) handleTranscribe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.transcriber == nil {
			httpError(w, http.StatusNotImplemented, fmt.Errorf("transcriber not configured"))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadSize)
		if err := r.ParseMultipartForm(h.maxUploadSize); err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("parse multipart form: %w", err))
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("missing file field: %w", err))
			return
		}
		defer file.Close()

		text, err := h.transcriber.Transcribe(r.Context(), header.Filename, file)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]string{"text": text})
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
