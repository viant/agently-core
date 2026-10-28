package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	requestctx "github.com/viant/forge/backend/requestctx"
)

const maxToolStreamFrame = 64 << 20
const maxToolProgressFrame = 16 << 10

func wantsToolStream(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept"), ",") {
		media, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err == nil && media == "text/event-stream" && params["q"] != "0" {
			return true
		}
	}
	return false
}

// executeToolStream shares the JSON endpoint's context and execution, without
// replay. Until the first progress event, failures retain ordinary HTTP status.
func executeToolStream(w http.ResponseWriter, r *http.Request, ctx context.Context, execute func(context.Context) (string, error), finishTiming func(http.ResponseWriter)) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("streaming unavailable"))
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	started, closed := false, false
	send := func(event string, raw []byte) error {
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
			// Timing finishes after execution; advertise it as an HTTP trailer.
			w.Header().Add("Trailer", "Server-Timing")
			started = true
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
		_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
		if err == nil {
			flusher.Flush()
		} else {
			cancel()
		}
		return err
	}
	ctx = requestctx.WithToolProgress(ctx, func(raw json.RawMessage) {
		mu.Lock()
		defer mu.Unlock()
		if closed || ctx.Err() != nil || len(raw) > maxToolProgressFrame || !json.Valid(raw) {
			return
		}
		_ = send("progress", raw)
	})
	if r.Header.Get("X-Agently-Tool-Updates") == "true" {
		ctx = requestctx.WithToolUpdates(ctx, func(raw json.RawMessage) error {
			mu.Lock()
			defer mu.Unlock()
			if closed {
				return fmt.Errorf("tool stream is closed")
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(raw) > maxToolStreamFrame || !json.Valid(raw) {
				return fmt.Errorf("tool update exceeds stream limit or is invalid")
			}
			return send("update", raw)
		})
	}
	result, err := execute(ctx)
	mu.Lock()
	defer mu.Unlock()
	closed = true
	finishTiming(w)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		if !started {
			writeToolExecuteError(w, err, result)
			return
		}
		// Reuse exactly the JSON path's authorization sanitization and error body.
		status, safeError, safeResult := toolExecuteErrorResponse(err, result)
		body := map[string]interface{}{"status": status, "error": safeError.Error()}
		if strings.TrimSpace(safeResult) != "" {
			body["result"] = safeResult
		}
		raw, _ := json.Marshal(body)
		if len(raw) > maxToolStreamFrame {
			raw = []byte(`{"status":500,"error":"tool error exceeds stream limit"}`)
		}
		_ = send("error", raw)
		return
	}
	raw, _ := json.Marshal(map[string]string{"result": result})
	if len(raw) > maxToolStreamFrame {
		_ = send("error", []byte(`{"status":500,"error":"tool result exceeds stream limit"}`))
		return
	}
	_ = send("result", raw)
}
