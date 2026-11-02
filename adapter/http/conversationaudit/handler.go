// Package conversationaudit serves the internal web UI audit API.
package conversationaudit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	audit "github.com/viant/agently-core/service/conversationaudit"
)

func New(s *audit.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/api/conversation-audit/capabilities", func(w http.ResponseWriter, r *http.Request) { v, e := s.Capabilities(r.Context()); respond(w, v, e) })
	mux.HandleFunc("GET /v1/api/conversation-audit/turns", func(w http.ResponseWriter, r *http.Request) {
		f, e := queryFilter(r)
		if e != nil {
			respond(w, nil, e)
			return
		}
		v, e := s.List(r.Context(), f)
		respond(w, v, e)
	})
	mux.HandleFunc("GET /v1/api/conversation-audit/conversations/{conversationId}/turns/{turnId}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Detail(r.Context(), r.PathValue("conversationId"), r.PathValue("turnId"))
		respond(w, v, e)
	})
	mux.HandleFunc("GET /v1/api/conversation-audit/conversations/{conversationId}/turns/{turnId}/payloads/{payloadId}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Payload(r.Context(), r.PathValue("conversationId"), r.PathValue("turnId"), r.PathValue("payloadId"))
		respond(w, v, e)
	})
	mux.HandleFunc("POST /v1/api/conversation-audit/export", func(w http.ResponseWriter, r *http.Request) {
		var f audit.Filter
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&f) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			respond(w, nil, audit.ErrFilter)
			return
		}
		data, e := s.CSV(r.Context(), f)
		if e != nil {
			respond(w, nil, e)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="conversation-audit.csv"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func queryFilter(r *http.Request) (audit.Filter, error) {
	q := r.URL.Query()
	f := audit.Filter{ConversationID: q.Get("conversationId"), UserID: q.Get("userId"), Cursor: q.Get("cursor")}
	allowed := map[string]bool{"conversationId": true, "userId": true, "from": true, "to": true, "cursor": true, "limit": true, "includeContent": true}
	for k, values := range q {
		if !allowed[k] || len(values) != 1 {
			return f, audit.ErrFilter
		}
	}
	if values, present := q["includeContent"]; present {
		switch values[0] {
		case "true":
			f.IncludeContent = true
		case "false":
		default:
			return f, audit.ErrFilter
		}
	}
	for _, item := range []struct {
		key    string
		target **time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if raw := q.Get(item.key); raw != "" {
			t, e := time.Parse(time.RFC3339Nano, raw)
			if e != nil {
				return f, audit.ErrFilter
			}
			*item.target = &t
		}
	}
	if raw := q.Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil {
			return f, audit.ErrFilter
		}
		f.Limit = n
	}
	return f, nil
}

func respond(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err == nil {
		_ = json.NewEncoder(w).Encode(value)
		return
	}
	status, message := http.StatusInternalServerError, "Could not read saved conversation audit."
	switch {
	case errors.Is(err, audit.ErrFilter):
		status, message = 400, err.Error()
	case errors.Is(err, audit.ErrForbidden):
		status, message = 403, audit.ErrForbidden.Error()
	case errors.Is(err, audit.ErrNotFound):
		status, message = 404, audit.ErrNotFound.Error()
	case errors.Is(err, audit.ErrTooLarge):
		status, message = 413, audit.ErrTooLarge.Error()
	case errors.Is(err, audit.ErrBusy):
		status, message = 429, audit.ErrBusy.Error()
		w.Header().Set("Retry-After", "5")
	case errors.Is(err, context.DeadlineExceeded):
		status, message = 504, "Audit read timed out; narrow the filters."
	case errors.Is(err, context.Canceled):
		status, message = 408, "Audit read canceled."
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
