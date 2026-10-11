// agui_probe is an isolated Endly observation adapter, not an application API.
// It uses the supported SDK and exposes actual canonical run evidence.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/viant/agently-core/e2e/assertion/colorword"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/sdk"
	"github.com/viant/agently-core/sdk/api"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type attachment struct {
	ThreadID     string `json:"threadId"`
	RunID        string `json:"runId"`
	AfterEventID string `json:"afterEventId,omitempty"`
}
type probeRequest struct {
	Attach         *attachment          `json:"attach,omitempty"`
	Target         string               `json:"target"`
	ProbeID        string               `json:"probeId"`
	Query          *agentsvc.QueryInput `json:"query,omitempty"`
	Input          *agui.RunAgentInput  `json:"input,omitempty"`
	ConversationID string               `json:"nativeConversationId,omitempty"`
}
type transportObservation struct {
	Method     string                           `json:"method"`
	Path       string                           `json:"path"`
	StatusCode int                              `json:"statusCode"`
	Metadata   *conversationMetadataObservation `json:"conversationMetadata,omitempty"`
}
type observer struct {
	next     http.RoundTripper
	mu       sync.Mutex
	requests []transportObservation
}

func (o *observer) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := o.next.RoundTrip(r)
	if response != nil {
		o.mu.Lock()
		index := len(o.requests)
		id, captureMetadata := metadataRequestID(r)
		observedPath := r.URL.Path
		if captureMetadata {
			observedPath = "/v1/conversations/{id}"
		}
		o.requests = append(o.requests, transportObservation{Method: r.Method, Path: observedPath, StatusCode: response.StatusCode})
		o.mu.Unlock()
		if captureMetadata && response.Body != nil {
			response.Body = &metadataCaptureBody{original: response.Body, requestedID: id, publish: func(value conversationMetadataObservation) {
				o.mu.Lock()
				o.requests[index].Metadata = &value
				o.mu.Unlock()
			}}
		}
	}
	return response, err
}
func (o *observer) observed() []transportObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]transportObservation(nil), o.requests...)
}

type identity struct {
	ThreadID        string `json:"threadId"`
	RunID           string `json:"runId"`
	ConversationID  string `json:"nativeConversationId"`
	TurnID          string `json:"nativeTurnId,omitempty"`
	ClientMessageID string `json:"clientMessageId,omitempty"`
}
type observedRun struct {
	mu        sync.Mutex
	ownerHash string
	target    string
	identity  identity
	deadline  time.Time
	events    []json.RawMessage
	queue     *agui.QueueValue
	approvals map[string]approvalTuple
}
type result struct {
	RedColorWordValid   bool                           `json:"redColorWordValid"`
	Identity            identity                       `json:"identity"`
	RootStreamText      string                         `json:"rootStreamText,omitempty"`
	AssistantText       string                         `json:"assistantText"`
	LiteralFinalContent string                         `json:"literalFinalContent,omitempty"`
	Outcome             sdk.AGUIRunOutcome             `json:"outcome"`
	Events              []json.RawMessage              `json:"events"`
	LastEventID         string                         `json:"lastEventId,omitempty"`
	HTTP                []transportObservation         `json:"http"`
	Transcript          *sdk.ConversationStateResponse `json:"transcript,omitempty"`
	RootTurn            *api.TurnState                 `json:"rootTurn,omitempty"`
	Error               *probeError                    `json:"error,omitempty"`
}
type probeError struct {
	Stage    string `json:"stage"`
	Category string `json:"category"`
	Message  string `json:"message"`
}
type handler struct {
	mu      sync.Mutex
	runs    map[string]*observedRun
	targets map[string]bool
}

func token(r *http.Request) string {
	return strings.TrimPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ")
}
func digest(value string) string { v := sha256.Sum256([]byte(value)); return hex.EncodeToString(v[:]) }
func newClient(target, bearer string, observed *observer) (*sdk.HTTPClient, error) {
	return sdk.NewHTTP(target, sdk.WithAuthToken(bearer), sdk.WithHTTPClient(&http.Client{Transport: observed, Timeout: 240 * time.Second}))
}
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/linked-review/") {
		h.linkedReview(w, r)
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/preview-metadata/") {
		h.preview(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
		write(w, 200, map[string]string{"status": "ok"})
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/approval-capture/") {
		h.approval(w, r, true)
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/approval-receipt/") {
		h.approval(w, r, false)
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/await-native/") {
		h.awaitNative(w, r)
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/observe/") {
		h.observe(w, r)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/run" {
		http.NotFound(w, r)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	var input probeRequest
	if err := decoder.Decode(&input); err != nil {
		write(w, 400, &probeError{"input", "invalid-input", "invalid probe input"})
		return
	}
	if !h.targets[input.Target] {
		write(w, 400, &probeError{"input", "invalid-target", "only reviewed owned app targets are allowed"})
		return
	}
	modes := 0
	if input.Query != nil {
		modes++
	}
	if input.Input != nil {
		modes++
	}
	if input.Attach != nil {
		modes++
	}
	if modes != 1 || input.ProbeID == "" {
		write(w, 400, &probeError{"input", "invalid-input", "exactly one canonical input mode and probeId required"})
		return
	}
	entry := &observedRun{ownerHash: digest(token(r)), target: input.Target, deadline: time.Now().Add(115 * time.Second), events: []json.RawMessage{}}
	h.mu.Lock()
	if _, exists := h.runs[input.ProbeID]; exists {
		h.mu.Unlock()
		write(w, 409, &probeError{"input", "duplicate-probe-id", "existing observation cannot redispatch"})
		return
	}
	h.runs[input.ProbeID] = entry
	h.mu.Unlock()
	observed := &observer{next: http.DefaultTransport}
	client, err := newClient(input.Target, token(r), observed)
	if err != nil {
		write(w, 400, &probeError{"client", "configuration", "SDK client initialization failed"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 235*time.Second)
	defer cancel()
	var canonical *agui.RunAgentInput
	conversationID := input.ConversationID
	if input.Query != nil {
		query, e := prepareInlineAttachments(ctx, client, input.Query)
		if e != nil {
			write(w, upstreamFailureStatus(observed.observed()), result{HTTP: observed.observed(), Error: classify("attachment", e)})
			return
		}
		prepared, e := client.PrepareAGUIChat(ctx, query)
		if e != nil {
			write(w, upstreamFailureStatus(observed.observed()), result{HTTP: observed.observed(), Error: classify("prepare", e)})
			return
		}
		canonical = &prepared.Input
		conversationID = prepared.ConversationID
	} else if input.Attach != nil {
		canonical = &agui.RunAgentInput{ThreadID: input.Attach.ThreadID, RunID: input.Attach.RunID}
	} else {
		canonical = input.Input
	}
	entry.mu.Lock()
	entry.identity = identity{ThreadID: canonical.ThreadID, RunID: canonical.RunID, ConversationID: conversationID}
	if len(canonical.Messages) > 0 {
		entry.identity.ClientMessageID = canonical.Messages[len(canonical.Messages)-1].ID
	}
	entry.mu.Unlock()
	var stream *sdk.AGUIRunStream
	if input.Attach != nil {
		stream, err = client.AttachAGUI(ctx, input.Attach.ThreadID, input.Attach.RunID, input.Attach.AfterEventID)
	} else {
		stream, err = client.RunAGUI(ctx, canonical, nil)
	}
	if err != nil {
		write(w, upstreamFailureStatus(observed.observed()), result{Identity: entry.identity, HTTP: observed.observed(), Error: classify("dispatch", err)})
		return
	}
	defer stream.Close()
	actual, collectErr := sdk.CollectAGUI(stream, conversationID, func(event sdk.AGUIEvent) error {
		entry.mu.Lock()
		defer entry.mu.Unlock()
		entry.events = append(entry.events, append(json.RawMessage(nil), event.Data...))
		if event.Type == "CUSTOM" {
			var frame struct {
				Name  string          `json:"name"`
				Value agui.QueueValue `json:"value"`
			}
			if json.Unmarshal(event.Data, &frame) == nil && frame.Name == "agently.queue" {
				value := frame.Value
				entry.queue = &value
			}
		}
		if event.Type == "RUN_STARTED" {
			var frame struct {
				Metadata struct {
					Agently struct {
						NativeTurnID string `json:"nativeTurnId"`
					} `json:"agently"`
				} `json:"metadata"`
			}
			if json.Unmarshal(event.Data, &frame) == nil {
				entry.identity.TurnID = frame.Metadata.Agently.NativeTurnID
			}
		}
		return nil
	})
	entry.mu.Lock()
	out := result{Identity: entry.identity, Events: append([]json.RawMessage(nil), entry.events...), HTTP: observed.observed()}
	entry.mu.Unlock()
	if actual != nil {
		out.Identity.TurnID = actual.TurnID
		out.RootStreamText = actual.Content
		out.AssistantText = actual.Content
		out.Outcome = actual.Outcome
		out.LastEventID = actual.LastEventID
	}
	if collectErr != nil {
		out.Error = classify("observe", collectErr)
	}
	// Canonical transcript is independently read through the same authenticated SDK.
	if conversationID != "" {
		transcript, e := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: conversationID, IncludeModelCalls: true, IncludeToolCalls: true})
		if e != nil {
			if out.Error == nil {
				out.Error = classify("transcript", e)
			}
		} else {
			out.Transcript = transcript
			if transcript != nil && transcript.Conversation != nil && actual != nil {
				for _, turn := range transcript.Conversation.Turns {
					if turn.TurnID == actual.TurnID {
						out.RootTurn = turn
						if turn.Assistant != nil && turn.Assistant.Final != nil {
							out.AssistantText = turn.Assistant.Final.Content
						}
						break
					}
				}
			}
		}
	}
	out.HTTP = observed.observed()
	out.RedColorWordValid = colorword.Matches(out.AssistantText, "red")
	out.LiteralFinalContent = literalNativeFinal(out.AssistantText)
	write(w, 200, out)
}
func (h *handler) observe(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/observe/")
	h.mu.Lock()
	record := h.runs[id]
	h.mu.Unlock()
	if record == nil || record.ownerHash != digest(token(r)) {
		http.NotFound(w, r)
		return
	}
	record.mu.Lock()
	actual := record.identity
	target := record.target
	var queue *agui.QueueValue
	if record.queue != nil {
		value := *record.queue
		queue = &value
	}
	events := append([]json.RawMessage(nil), record.events...)
	record.mu.Unlock()
	client, err := newClient(target, token(r), &observer{next: http.DefaultTransport})
	if err != nil {
		http.Error(w, "client initialization failed", 500)
		return
	}
	if actual.ConversationID != "" {
		if _, err = client.GetConversation(r.Context(), actual.ConversationID); err != nil {
			write(w, 403, &probeError{"observe", "read-denied", "current principal cannot read the observed conversation"})
			return
		}
	}
	snapshot, err := client.GetTranscript(r.Context(), &sdk.GetTranscriptInput{ConversationID: actual.ConversationID, IncludeModelCalls: true, IncludeToolCalls: true})
	if err != nil {
		write(w, 502, &probeError{"observe", "transcript-read-failed", "cannot read original transcript"})
		return
	}
	var root *api.TurnState
	if snapshot != nil && snapshot.Conversation != nil {
		for _, turn := range snapshot.Conversation.Turns {
			if turn != nil && turn.TurnID == actual.TurnID {
				root = turn
				break
			}
		}
	}
	if root == nil {
		write(w, 409, &probeError{"observe", "original-turn-missing", "observed native turn absent from current transcript"})
		return
	}
	var tools []*api.ToolStepState
	if root.Execution != nil {
		for _, page := range root.Execution.Pages {
			if page != nil {
				tools = append(tools, page.ToolSteps...)
			}
		}
	}
	pending, err := client.ListPendingElicitations(r.Context(), &sdk.ListPendingElicitationsInput{ConversationID: actual.ConversationID})
	if err != nil {
		write(w, 502, &probeError{"observe", "pending-read-failed", "cannot read original pending elicitations"})
		return
	}
	messages, err := client.GetMessages(r.Context(), &sdk.GetMessagesInput{ConversationID: actual.ConversationID, TurnID: actual.TurnID, Types: []string{"control"}})
	if err != nil {
		write(w, 502, &probeError{"observe", "control-read-failed", "cannot read original control messages"})
		return
	}
	write(w, 200, map[string]any{"identity": actual, "queue": queue, "events": events, "rootTurn": root, "toolSteps": tools, "pendingElicitations": pending, "controlMessages": messages.Rows})
}

// awaitNative observes the original native turn; it never admits another run.
func (h *handler) awaitNative(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/await-native/")
	h.mu.Lock()
	record := h.runs[id]
	h.mu.Unlock()
	if record == nil || record.ownerHash != digest(token(r)) {
		http.NotFound(w, r)
		return
	}
	record.mu.Lock()
	actual, target, deadline := record.identity, record.target, record.deadline
	record.mu.Unlock()
	if actual.TurnID == "" || actual.ConversationID == "" {
		write(w, 409, &probeError{"await-native", "identity-not-observed", "original native turn identity unavailable"})
		return
	}
	// Five seconds of headroom within the original Endly120s request budget.
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	client, err := newClient(target, token(r), &observer{next: http.DefaultTransport})
	if err != nil {
		write(w, 500, &probeError{"await-native", "client", "SDK initialization failed"})
		return
	}
	if _, err = client.GetConversation(ctx, actual.ConversationID); err != nil {
		if observationBudgetExpired(ctx, err) {
			write(w, http.StatusGatewayTimeout, &probeError{"await-native", "observation-timeout", "original observation budget expired"})
			return
		}
		write(w, 403, &probeError{"await-native", "read-denied", "current owner cannot read original conversation"})
		return
	}
	turn, err := awaitExactTurn(ctx, actual.TurnID, time.Second, func(ctx context.Context) (*sdk.ConversationStateResponse, error) {
		return client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: actual.ConversationID, IncludeModelCalls: true, IncludeToolCalls: true})
	})
	if err != nil {
		write(w, 504, &probeError{"await-native", "observation-failed", "original native turn did not reach an observable terminal state within its original budget"})
		return
	}
	if turn.Status != api.TurnStatusCompleted {
		write(w, 409, map[string]any{"identity": actual, "nativeStatus": turn.Status, "error": &probeError{"await-native", "native-not-completed", "original native turn terminated without completion"}})
		return
	}
	if turn.Assistant == nil || turn.Assistant.Final == nil || turn.Assistant.Final.Content == "" {
		write(w, 409, &probeError{"await-native", "terminal-without-final", "completed original native turn lacks actual final content"})
		return
	}
	write(w, 200, map[string]any{"identity": actual, "nativeStatus": turn.Status, "rootTurn": turn, "assistantText": turn.Assistant.Final.Content, "literalFinalContent": literalNativeFinal(turn.Assistant.Final.Content)})
}
func awaitExactTurn(ctx context.Context, turnID string, interval time.Duration, read func(context.Context) (*sdk.ConversationStateResponse, error)) (*api.TurnState, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		snapshot, err := read(ctx)
		if err != nil {
			return nil, err
		}
		if snapshot != nil && snapshot.Conversation != nil {
			for _, turn := range snapshot.Conversation.Turns {
				if turn != nil && turn.TurnID == turnID && (turn.Status == api.TurnStatusCompleted || turn.Status == api.TurnStatusFailed || turn.Status == api.TurnStatusCanceled) {
					return turn, nil
				}
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
func classify(stage string, err error) *probeError {
	category := "sdk-error"
	var runError *sdk.AGUIRunError
	var observationError *sdk.AGUIObservationError
	if errors.As(err, &runError) {
		category = runError.Code
	} else if errors.As(err, &observationError) {
		category = "uncertain-observation"
	}
	message := err.Error()
	if len(message) > 600 {
		message = message[:600]
	}
	return &probeError{stage, category, message}
}
func upstreamFailureStatus(requests []transportObservation) int {
	for i := len(requests) - 1; i >= 0; i-- {
		if requests[i].StatusCode >= 400 {
			return requests[i].StatusCode
		}
	}
	return 400
}
func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func main() {
	port := flag.Int("port", 29516, "owned isolated probe port")
	appTarget := flag.String("app-target", "http://127.0.0.1:29501", "exact owned application target")
	authTarget := flag.String("auth-target", "http://127.0.0.1:29502", "exact owned authenticated application target")
	flag.Parse()
	targets, err := ownedTargets(*appTarget, *authTarget)
	if err != nil || *port < 1024 || *port > 65535 {
		panic("invalid owned probe configuration")
	}
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", *port), Handler: &handler{runs: map[string]*observedRun{}, targets: targets}, ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic("owned AGUI observation server failed")
	}
}

const nativeAssistantPrefix = "Native assistant output:\n"

func literalNativeFinal(content string) string {
	if content == "" {
		return ""
	}
	return nativeAssistantPrefix + content
}

func observationBudgetExpired(ctx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded))
}

func ownedTargets(values ...string) (map[string]bool, error) {
	targets := map[string]bool{}
	for _, value := range values {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("exact loopback target required")
		}
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1024 || port > 65535 {
			return nil, fmt.Errorf("owned target port required")
		}
		targets[value] = true
	}
	return targets, nil
}
