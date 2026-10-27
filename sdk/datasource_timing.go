package sdk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/viant/agently-core/protocol/primitive/observe"
)

// datasourceTimingPhaseNames is an allowlist: request values, provider names,
// resource identifiers, credentials, and error messages never enter headers.
var datasourceTimingPhaseNames = []struct {
	phase observe.Phase
	name  string
}{
	{"report.resource.entry", "report_entry"},
	{"report.resource.total", "report_total"},
	{"report.resource.select", "report_select"},
	{"report.resource.compile", "report_compile"},
	{"report.resource.precheck", "report_precheck"},
	{"report.resource.plan", "report_plan"},
	{"report.resource.fetch", "report_fetch"},
	{"report.resource.materialize", "report_materialize"},
	{"report.resource.final", "report_final"},
	{"datasource.client.fetch", "ds_client"},
	{"datasource.client.resolve", "ds_resolve"},
	{"datasource.service.fetch", "ds_service"},
	{"datasource.service.backend", "ds_backend"},
	{"datasource.published.total", "ds_published"},
	{"datasource.published.backend", "ds_published_backend"},
	{"window.binding.owner", "binding_owner"},
	{"artifact.admit", "cohort_admit"},
	{"artifact.root.open", "cohort_open"},
	{"artifact.store.get", "cohort_store"},
	{"artifact.window.open", "cohort_window"},
	{"artifact.window.decode", "cohort_decode"},
	{"gateway.binding", "gw_binding"},
	{"gateway.provider.discovery", "gw_discovery"},
	{"gateway.definition.read", "gw_read"},
	{"gateway.resource.list", "resource_list"},
	{"gateway.resource.prepare", "resource_prepare"},
	{"gateway.resource.read", "resource_read"},
	{"gateway.resource.decode", "resource_decode"},
	{"gateway.mcp.call", "gw_mcp"},
	{"gateway.request.encode", "gw_encode"},
	{"gateway.response.decode", "gw_decode"},
	{"mcp.pool.acquire", "pool_acquire"},
	{"mcp.pool.create", "pool_create"},
	{"mcp.pool.wait", "pool_wait"},
	{"mcp.auth.user", "auth_user"},
	{"mcp.auth.user.lookup", "auth_user_lookup"},
	{"mcp.auth.provider", "auth_provider"},
	{"mcp.auth.schema", "auth_schema"},
	{"mcp.auth.store", "auth_store"},
	{"mcp.auth.decrypt", "auth_decrypt"},
	{"mcp.auth.validate", "auth_validate"},
	{"mcp.auth.refresh", "auth_refresh"},
	{"mcp.configuration", "mcp_config"},
	{"mcp.dispatch", "mcp_dispatch"},
	{"tool.registry.execute", "tool_execute"},
	{"mcp.tool.call", "tool_call"},
	{"mcp.tool.decode", "tool_decode"},
}

type datasourceTimingAggregate struct {
	count int
	total time.Duration
	max   time.Duration
}

type datasourceTimingCollector struct {
	mu     sync.Mutex
	events map[observe.Phase]datasourceTimingAggregate
	trace  bool
	all    map[observe.Phase]datasourceTraceAggregate
}

type datasourceTraceAggregate struct {
	Calls  int     `json:"calls"`
	MS     float64 `json:"ms"`
	MaxMS  float64 `json:"maxMs"`
	Hit    int     `json:"hit,omitempty"`
	Miss   int     `json:"miss,omitempty"`
	Bypass int     `json:"bypass,omitempty"`
	Errors int     `json:"errors,omitempty"`
	Bytes  int64   `json:"bytes,omitempty"`
}

func (c *datasourceTimingCollector) Observe(event observe.Event) {
	if c == nil {
		return
	}
	if c.trace && safeDatasourceTracePhase(event.Phase) {
		c.mu.Lock()
		if c.all == nil {
			c.all = map[observe.Phase]datasourceTraceAggregate{}
		}
		entry := c.all[event.Phase]
		entry.Calls++
		entry.MS += milliseconds(event.Duration)
		entry.MaxMS = max(entry.MaxMS, milliseconds(event.Duration))
		entry.Bytes += event.Bytes
		switch event.Cache {
		case observe.CacheHit:
			entry.Hit++
		case observe.CacheMiss:
			entry.Miss++
		case observe.CacheBypass:
			entry.Bypass++
		}
		if event.Outcome == observe.Error || event.Outcome == observe.Denied || event.Outcome == observe.Canceled {
			entry.Errors++
		}
		c.all[event.Phase] = entry
		c.mu.Unlock()
	}
	allowed := false
	for _, entry := range datasourceTimingPhaseNames {
		if event.Phase == entry.phase {
			allowed = true
			break
		}
	}
	if !allowed {
		return
	}
	c.mu.Lock()
	if c.events == nil {
		c.events = make(map[observe.Phase]datasourceTimingAggregate, len(datasourceTimingPhaseNames))
	}
	value := c.events[event.Phase]
	value.count++
	value.total += event.Duration
	if event.Duration > value.max {
		value.max = event.Duration
	}
	c.events[event.Phase] = value
	c.mu.Unlock()
}

func safeDatasourceTracePhase(phase observe.Phase) bool {
	value := string(phase)
	if len(value) == 0 || len(value) > 80 {
		return false
	}
	allowed := false
	for _, prefix := range []string{"artifact.", "catalog.", "datasource.", "gateway.", "manager.", "mcp.", "tool.", "window.", "report."} {
		if strings.HasPrefix(value, prefix) {
			allowed = true
			break
		}
	}
	if !allowed {
		return false
	}
	for _, char := range value {
		if char != '.' && char != '_' && char != '-' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

// traceJSON excludes event labels and request contents. It is only written to
// the local diagnostic log when BACKEND_PRIMITIVE_TRACE is explicitly enabled.
func (c *datasourceTimingCollector) traceJSON() string {
	if c == nil || !c.trace {
		return "{}"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.all))
	for phase := range c.all {
		names = append(names, string(phase))
	}
	sort.Strings(names)
	value := make(map[string]datasourceTraceAggregate, len(names))
	for _, name := range names {
		value[name] = c.all[observe.Phase(name)]
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func (c *datasourceTimingCollector) header(elapsed time.Duration) string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	parts := []string{fmt.Sprintf("ds_handler;dur=%.3f", milliseconds(elapsed))}
	for _, entry := range datasourceTimingPhaseNames {
		value := c.events[entry.phase]
		if value.count == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s;dur=%.3f;desc=\"count=%d\"", entry.name, milliseconds(value.total), value.count))
		if value.count > 1 {
			parts = append(parts, fmt.Sprintf("%s_max;dur=%.3f", entry.name, milliseconds(value.max)))
		}
	}
	return strings.Join(parts, ", ")
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func setDatasourceTimingHeader(w http.ResponseWriter, collector *datasourceTimingCollector, started time.Time) {
	if collector != nil {
		w.Header().Set("Server-Timing", collector.header(time.Since(started)))
	}
}
