package sdk

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/viant/agently-core/protocol/primitive/observe"
)

// Reuse the numeric phase collector at the generic tool HTTP boundary. With
// diagnostics disabled, the original request is returned without an observer.
func prepareToolTiming(r *http.Request) (*http.Request, func(http.ResponseWriter)) {
	trace := os.Getenv("BACKEND_PRIMITIVE_TRACE") == "1"
	perf := r.URL.Query().Get("perf") == "1"
	if !trace && !perf {
		return r, func(http.ResponseWriter) {}
	}
	started := time.Now()
	collector := &datasourceTimingCollector{trace: trace}
	r = r.WithContext(observe.WithObserver(r.Context(), collector))
	return r, func(w http.ResponseWriter) {
		if perf {
			w.Header().Set("Server-Timing", strings.Replace(collector.header(time.Since(started)), "ds_handler;", "tool_handler;", 1))
		}
		if trace {
			log.Printf("primitive tool timing (nested spans overlap): %s", collector.traceJSON())
		}
	}
}
