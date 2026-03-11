package scheduler

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Handler serves scheduler HTTP endpoints.
type Handler struct {
	svc *Service
}

// NewHandler creates a scheduler HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Register mounts all scheduler routes on the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/api/agently/scheduler/schedule/{id}", h.handleGetSchedule())
	mux.HandleFunc("GET /v1/api/agently/scheduler/", h.handleListSchedules())
	mux.HandleFunc("PATCH /v1/api/agently/scheduler/", h.handleBatchUpdate())
	mux.HandleFunc("POST /v1/api/agently/scheduler/run-now/{id}", h.handleRunNow())
}

// RegisterWithoutRunNow mounts scheduler routes except the run-now endpoint.
func (h *Handler) RegisterWithoutRunNow(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/api/agently/scheduler/schedule/{id}", h.handleGetSchedule())
	mux.HandleFunc("GET /v1/api/agently/scheduler/", h.handleListSchedules())
	mux.HandleFunc("PATCH /v1/api/agently/scheduler/", h.handleBatchUpdate())
}

func (h *Handler) handleGetSchedule() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("schedule ID is required"))
			return
		}
		sched, err := h.svc.Get(r.Context(), id)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		if sched == nil {
			httpError(w, http.StatusNotFound, fmt.Errorf("schedule %s not found", id))
			return
		}
		httpJSON(w, http.StatusOK, map[string]interface{}{
			"status": "ok",
			"data":   sched,
		})
	}
}

func (h *Handler) handleListSchedules() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := h.svc.List(r.Context())
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]interface{}{
			"status": "ok",
			"data": map[string]interface{}{
				"schedules": list,
			},
		})
	}
}

func (h *Handler) handleBatchUpdate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Schedules []*Schedule `json:"schedules"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		for _, s := range body.Schedules {
			if err := h.svc.Upsert(r.Context(), s); err != nil {
				httpError(w, http.StatusInternalServerError, err)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) handleRunNow() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("schedule ID is required"))
			return
		}
		if err := h.svc.RunNow(r.Context(), id); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "error",
		"error":  err.Error(),
	})
}
