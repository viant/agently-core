package sdk

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
)

// AGUIDemoBackend explicitly enables an anonymous, public interoperability
// endpoint. It is not a production credential/delegation configuration. Threads
// are principal-scoped, ephemeral, and cannot be recovered after BFF restart.
// Only synthetic, text-only conversations should be sent to public demos.
type AGUIDemoBackend struct {
	ID              string   `json:"id" yaml:"id"`
	Label           string   `json:"label" yaml:"label"`
	URL             string   `json:"-" yaml:"url"`
	AllowedSubjects []string `json:"-" yaml:"allowedSubjects"`
}

// WithAGUIDemoBackends installs trusted, explicitly authorized public demo
// endpoints behind existing BFF authentication. Empty subject lists are invalid.
func WithAGUIDemoBackends(backends ...AGUIDemoBackend) HandlerOption {
	return func(cfg *handlerConfig) {
		for _, backend := range backends {
			backend.AllowedSubjects = append([]string(nil), backend.AllowedSubjects...)
			cfg.aguiDemoBackends = append(cfg.aguiDemoBackends, backend)
		}
	}
}

type aguiDemoThread struct {
	principal, backend string
	expires            time.Time
	runs               map[string][32]byte
	active             bool
}

type aguiDemoRegistry struct {
	mu          sync.Mutex
	backends    map[string]AGUIDemoBackend
	threads     map[string]*aguiDemoThread
	client      *http.Client
	localReplay bool
}

func newAGUIDemoRegistry(configs []AGUIDemoBackend) (*aguiDemoRegistry, error) {
	r := &aguiDemoRegistry{backends: map[string]AGUIDemoBackend{}, threads: map[string]*aguiDemoThread{}, client: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	for _, cfg := range configs {
		if cfg.ID == "" || cfg.ID == "agently" || strings.ContainsAny(cfg.ID, "/?#%\\ \t\r\n") {
			return nil, fmt.Errorf("invalid AG-UI demo backend identity")
		}
		if _, exists := r.backends[cfg.ID]; exists {
			return nil, fmt.Errorf("duplicate AG-UI demo backend identity %q", cfg.ID)
		}
		u, err := url.Parse(cfg.URL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
			return nil, fmt.Errorf("AG-UI demo backend %q requires HTTPS or loopback HTTP without credentials", cfg.ID)
		}
		if len(cfg.AllowedSubjects) == 0 {
			return nil, fmt.Errorf("AG-UI demo backend %q requires explicit allowed subjects", cfg.ID)
		}
		for _, subject := range cfg.AllowedSubjects {
			if strings.TrimSpace(subject) == "" || subject == "*" {
				return nil, fmt.Errorf("AG-UI demo backend %q requires named subjects", cfg.ID)
			}
		}
		r.backends[cfg.ID] = cfg
	}
	return r, nil
}

func (s *aguiDemoRegistry) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/ag-ui/backends", s.list)
	mux.HandleFunc("POST /v1/ag-ui/backends/{backend}/threads", s.createThread)
	mux.HandleFunc("POST /v1/ag-ui/backends/{backend}/run", s.run)
}

func demoAllowed(backend AGUIDemoBackend, principal string) bool {
	for _, allowed := range backend.AllowedSubjects {
		if principal != "" && allowed == principal {
			return true
		}
	}
	return false
}

func (s *aguiDemoRegistry) list(w http.ResponseWriter, r *http.Request) {
	principal := iauth.EffectiveUserID(r.Context())
	// The outer BFF keeps its existing authentication policy. In explicitly
	// unauthenticated/local mode the built-in connection is still discoverable;
	// remote demo entries always require a named, authenticated subject.
	items := []map[string]any{{"id": "agently", "label": "Agently", "profile": "agently", "durableReplay": s.localReplay}}
	for _, backend := range s.backends {
		if demoAllowed(backend, principal) {
			items = append(items, map[string]any{"id": backend.ID, "label": backend.Label, "profile": "standard", "publicDemo": true, "ephemeral": true, "durableReplay": false, "inputMode": "text-only"})
		}
	}
	sort.Slice(items[1:], func(i, j int) bool { return items[i+1]["id"].(string) < items[j+1]["id"].(string) })
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"backends": items})
}

func (s *aguiDemoRegistry) authorized(w http.ResponseWriter, r *http.Request) (AGUIDemoBackend, string, bool) {
	principal := iauth.EffectiveUserID(r.Context())
	if principal == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return AGUIDemoBackend{}, "", false
	}
	backend, found := s.backends[r.PathValue("backend")]
	if !found || !demoAllowed(backend, principal) {
		http.Error(w, "backend unavailable", http.StatusForbidden)
		return AGUIDemoBackend{}, "", false
	}
	return backend, principal, true
}

func (s *aguiDemoRegistry) createThread(w http.ResponseWriter, r *http.Request) {
	backend, principal, ok := s.authorized(w, r)
	if !ok {
		return
	}
	if mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mediaType != "application/json" {
		http.Error(w, "JSON request required", http.StatusUnsupportedMediaType)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(data, &fields) != nil || len(fields) != 0 {
		http.Error(w, "thread creation accepts an empty object", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	now := time.Now()
	for id, thread := range s.threads {
		if !thread.active && now.After(thread.expires) {
			delete(s.threads, id)
		}
	}
	if len(s.threads) >= 10000 {
		s.mu.Unlock()
		http.Error(w, "demo thread capacity reached", http.StatusTooManyRequests)
		return
	}
	id := uuid.NewString()
	s.threads[id] = &aguiDemoThread{principal: principal, backend: backend.ID, expires: now.Add(24 * time.Hour), runs: map[string][32]byte{}}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"threadId": id, "connectionId": backend.ID, "ephemeral": true, "durableReplay": false})
}

func (s *aguiDemoRegistry) run(w http.ResponseWriter, r *http.Request) {
	backend, principal, ok := s.authorized(w, r)
	if !ok {
		return
	}
	if mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mediaType != "application/json" {
		http.Error(w, "JSON request required", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil || agui.ValidateInput(body) != nil {
		http.Error(w, "invalid AG-UI input", http.StatusBadRequest)
		return
	}
	var input agui.RunAgentInput
	if json.Unmarshal(body, &input) != nil || !demoTextInput(input) {
		http.Error(w, "public demo accepts standard text conversations only", http.StatusBadRequest)
		return
	}
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&normalized) != nil {
		http.Error(w, "invalid AG-UI input", http.StatusBadRequest)
		return
	}
	canonical, _ := json.Marshal(normalized)
	fingerprint := sha256.Sum256(canonical)
	s.mu.Lock()
	thread := s.threads[input.ThreadID]
	if thread == nil || thread.principal != principal || thread.backend != backend.ID || time.Now().After(thread.expires) {
		s.mu.Unlock()
		http.Error(w, "thread unavailable", http.StatusForbidden)
		return
	}
	if input.ParentRunID != "" {
		if _, found := thread.runs[input.ParentRunID]; !found {
			s.mu.Unlock()
			http.Error(w, "parent run unavailable", http.StatusForbidden)
			return
		}
	}
	if previous, exists := thread.runs[input.RunID]; exists {
		s.mu.Unlock()
		if previous != fingerprint {
			http.Error(w, "run input conflicts with prior submission", http.StatusConflict)
		} else {
			http.Error(w, "demo backend does not advertise durable replay", http.StatusConflict)
		}
		return
	}
	if thread.active || len(thread.runs) >= 256 {
		s.mu.Unlock()
		http.Error(w, "demo thread cannot accept another run", http.StatusConflict)
		return
	}
	thread.runs[input.RunID] = fingerprint
	thread.active = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); thread.active = false; s.mu.Unlock() }()
	// Forward the same normalized object used for admission. Different JSON
	// parsers must not disagree about duplicate identity keys in the raw body.
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, backend.URL, bytes.NewReader(canonical))
	if err != nil {
		http.Error(w, "backend request unavailable", http.StatusBadGateway)
		return
	}
	// Construct headers afresh. Never forward BFF authentication, cookies,
	// session/debug identity or arbitrary client headers to an anonymous demo.
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	response, err := s.client.Do(request)
	if err != nil {
		http.Error(w, "backend connection failed; execution outcome may be unknown", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		http.Error(w, "backend did not return an AG-UI event stream", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = io.Copy(demoFlushingWriter{w}, response.Body)
}

func demoTextInput(input agui.RunAgentInput) bool {
	if len(input.Tools) != 0 || len(input.Context) != 0 || len(input.Resume) != 0 {
		return false
	}
	if len(input.ForwardedProps) != 0 {
		var props map[string]json.RawMessage
		if json.Unmarshal(input.ForwardedProps, &props) != nil || len(props) != 0 {
			return false
		}
	}
	for _, message := range input.Messages {
		if message.Role != "user" && message.Role != "assistant" && message.Role != "system" {
			return false
		}
		var content string
		if len(message.Content) != 0 && json.Unmarshal(message.Content, &content) != nil {
			return false
		}
		var metadata map[string]json.RawMessage
		var calls []json.RawMessage
		if len(message.Metadata) > 0 && (json.Unmarshal(message.Metadata, &metadata) != nil || len(metadata) > 0) {
			return false
		}
		if (len(message.ToolCalls) > 0 && (json.Unmarshal(message.ToolCalls, &calls) != nil || len(calls) > 0)) || message.ToolCallID != "" {
			return false
		}
	}
	return true
}

type demoFlushingWriter struct{ http.ResponseWriter }

func (w demoFlushingWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if err == nil {
		err = http.NewResponseController(w.ResponseWriter).Flush()
	}
	return n, err
}
