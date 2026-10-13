package observe

import (
	"net/http"
	"sync"
	"time"
)

const (
	HTTPRequest             Phase = "http.total"
	HTTPWindowResolve       Phase = "http.window.resolve"
	HTTPWindowPermission    Phase = "http.window.permission"
	HTTPWindowBind          Phase = "http.window.bind"
	HTTPResponseEncode      Phase = "http.response.encode"
	GatewayDefinitionDecode Phase = "gateway.definition.decode"
)

type phaseTotal struct {
	duration        time.Duration
	calls, failures uint64
}

// Collector retains bounded phase totals only. Resource labels, request data,
// credentials and error text are intentionally not retained. Nested spans
// overlap, so the resulting durations must not be added into a wall-clock total.
type Collector struct {
	mu     sync.Mutex
	phases map[Phase]phaseTotal
}

func (c *Collector) Observe(event Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phases == nil {
		c.phases = make(map[Phase]phaseTotal)
	}
	value, exists := c.phases[event.Phase]
	if !exists && (len(c.phases) >= 64 || len(event.Phase) > 128) {
		return
	}
	value.duration += event.Duration
	value.calls++
	if event.Outcome != OK {
		value.failures++
	}
	c.phases[event.Phase] = value
}

// Totals returns detached diagnostic values in milliseconds and call counts.
func (c *Collector) Totals() map[string]float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[string]float64, len(c.phases)*3)
	for phase, value := range c.phases {
		key := string(phase)
		result[key+".ms"] = float64(value.duration) / float64(time.Millisecond)
		result[key+".calls"] = float64(value.calls)
		if value.failures > 0 {
			result[key+".errors"] = float64(value.failures)
		}
	}
	return result
}

// HTTP measures an opt-in request without wrapping ResponseWriter, so flushing,
// hijacking and streaming behavior are unchanged. A nil sink is a no-op.
func HTTP(next http.Handler, sink func(map[string]float64)) http.Handler {
	if sink == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		collector := &Collector{}
		ctx := WithObserver(r.Context(), collector)
		span := Start(ctx, HTTPRequest, Labels{})
		defer func() {
			span.End(OutcomeFor(ctx.Err()), CacheUnknown)
			sink(collector.Totals())
		}()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
