package scheduler

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	schrun "github.com/viant/agently-core/pkg/agently/scheduler/run"
	svcauth "github.com/viant/agently-core/service/auth"
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
	mux.HandleFunc("GET /v1/api/agently/scheduler/run", h.handleListRuns())
	mux.HandleFunc("GET /v1/api/agently/scheduler/run/", h.handleListRuns())
	mux.HandleFunc("GET /v1/api/agently/scheduler/run/{id}", h.handleListRuns())
	mux.HandleFunc("GET /v1/api/agently/scheduler/schedule/{id}", h.handleGetSchedule())
	mux.HandleFunc("GET /v1/api/agently/scheduler/", h.handleListSchedules())
	mux.HandleFunc("PATCH /v1/api/agently/scheduler/", h.handleBatchUpdate())
	mux.HandleFunc("POST /v1/api/agently/scheduler/run-now/{id}", h.handleRunNow())
}

// RegisterWithoutRunNow mounts scheduler routes except the run-now endpoint.
func (h *Handler) RegisterWithoutRunNow(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/api/agently/scheduler/run", h.handleListRuns())
	mux.HandleFunc("GET /v1/api/agently/scheduler/run/", h.handleListRuns())
	mux.HandleFunc("GET /v1/api/agently/scheduler/run/{id}", h.handleListRuns())
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
		page, size := parsePaging(r, 10)
		list, err := h.svc.List(r.Context())
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		totalCount := len(list)
		httpJSON(w, http.StatusOK, map[string]interface{}{
			"status": "ok",
			"data": map[string]interface{}{
				"schedules": paginateSchedules(list, page, size),
			},
			"info": map[string]interface{}{
				"pageCount":  pageCount(totalCount, size),
				"totalCount": totalCount,
			},
		})
	}
}

func (h *Handler) handleListRuns() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scheduleID := strings.TrimSpace(r.PathValue("id"))
		if scheduleID == "" {
			scheduleID = strings.TrimSpace(r.URL.Query().Get("scheduleId"))
		}
		status := strings.TrimSpace(r.URL.Query().Get("status"))
		requireScheduleID := isTruthy(r.URL.Query().Get("requireScheduleId"))
		page, size := parsePaging(r, 10)
		if requireScheduleID && scheduleID == "" {
			httpJSON(w, http.StatusOK, map[string]interface{}{
				"status": "ok",
				"data":   []*schrun.RunView{},
				"info": map[string]interface{}{
					"pageCount":  1,
					"totalCount": 0,
				},
			})
			return
		}
		input := &schrun.RunListInput{Has: &schrun.RunListInputHas{}}
		if userID := strings.TrimSpace(svcauth.EffectiveUserID(r.Context())); userID != "" {
			input.EffectiveUserID = userID
			input.Has.EffectiveUserID = true
		}
		if scheduleID != "" {
			input.ScheduleId = scheduleID
			input.Has.ScheduleId = true
		}
		if status != "" {
			input.RunStatus = status
			input.Has.RunStatus = true
		}
		result, err := h.svc.store.ListRuns(r.Context(), input, page, size)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]interface{}{
			"status": "ok",
			"data":   result.Rows,
			"info": map[string]interface{}{
				"pageCount":  result.PageCount,
				"totalCount": result.TotalCount,
			},
		})
	}
}

func parsePaging(r *http.Request, defaultSize int) (int, int) {
	page := 1
	size := defaultSize
	if r == nil || r.URL == nil {
		return page, size
	}
	if rawPage := strings.TrimSpace(r.URL.Query().Get("page")); rawPage != "" {
		if parsed, err := strconv.Atoi(rawPage); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if rawSize := strings.TrimSpace(r.URL.Query().Get("size")); rawSize != "" {
		if parsed, err := strconv.Atoi(rawSize); err == nil && parsed > 0 {
			size = parsed
		}
	}
	return page, size
}

func pageCount(totalCount, size int) int {
	if totalCount <= 0 || size <= 0 {
		return 1
	}
	return int(math.Max(1, math.Ceil(float64(totalCount)/float64(size))))
}

func paginateSchedules(schedules []*Schedule, page, size int) []*Schedule {
	if size <= 0 || page < 1 {
		return schedules
	}
	start := (page - 1) * size
	if start >= len(schedules) {
		return []*Schedule{}
	}
	end := start + size
	if end > len(schedules) {
		end = len(schedules)
	}
	return schedules[start:end]
}

func isTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
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
