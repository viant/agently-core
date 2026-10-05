package sdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/viant/agently-core/protocol/agui"
)

// AGUIEvent retains the complete wire event, including extension metadata and
// integer spellings, along with its opaque durable SSE cursor.
type AGUIEvent struct {
	ID   string
	Type string
	Data json.RawMessage
}

func (e AGUIEvent) Decode(target any) error { return json.Unmarshal(e.Data, target) }

type AGUIRunOptions struct{ AfterEventID string }

// AGUIObservationError means the connection ended without an authoritative root
// terminal event. Attach using these identities; never resubmit a fresh prompt.
type AGUIObservationError struct {
	ThreadID, RunID, LastEventID string
	Cause                        error
}

func (e *AGUIObservationError) Error() string {
	return "AG-UI observation interrupted; attach to the existing run to determine its outcome"
}
func (e *AGUIObservationError) Unwrap() error { return e.Cause }

type AGUIRunStream struct {
	ThreadID, RunID string
	body            io.ReadCloser
	reader          *bufio.Reader
	mu              sync.Mutex
	lastID          string
	terminal        bool
	eof             bool
}

func (s *AGUIRunStream) LastEventID() string { s.mu.Lock(); defer s.mu.Unlock(); return s.lastID }
func (s *AGUIRunStream) Close() error {
	if s == nil || s.body == nil {
		return nil
	}
	return s.body.Close()
}
func (s *AGUIRunStream) observationError(cause error) error {
	return &AGUIObservationError{ThreadID: s.ThreadID, RunID: s.RunID, LastEventID: s.LastEventID(), Cause: cause}
}

// Recv has a single consumer. Close may interrupt a blocked read. It supports
// multiline/large SSE events without Scanner's implicit token-size ceiling.
func (s *AGUIRunStream) Recv() (AGUIEvent, error) {
	if s == nil || s.reader == nil {
		return AGUIEvent{}, fmt.Errorf("AG-UI stream unavailable")
	}
	if s.terminal {
		return AGUIEvent{}, io.EOF
	}
	if s.eof {
		return AGUIEvent{}, s.observationError(io.ErrUnexpectedEOF)
	}
	var data strings.Builder
	id := ""
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			s.eof = true
			if !errors.Is(err, io.EOF) {
				return AGUIEvent{}, s.observationError(err)
			}
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" || s.eof {
			if line != "" {
				appendAGUILine(line, &data, &id)
			}
			if data.Len() > 0 {
				raw := strings.TrimSuffix(data.String(), "\n")
				var header struct {
					Type, ThreadID, RunID string
					SubagentRunID         *string `json:"subagentRunId"`
				}
				if !json.Valid([]byte(raw)) || json.Unmarshal([]byte(raw), &header) != nil || header.Type == "" {
					return AGUIEvent{}, s.observationError(fmt.Errorf("invalid AG-UI event"))
				}
				if id != "" {
					s.mu.Lock()
					s.lastID = id
					s.mu.Unlock()
				}
				root := header.SubagentRunID == nil && (header.RunID == "" || header.RunID == s.RunID) && (header.ThreadID == "" || header.ThreadID == s.ThreadID)
				s.terminal = root && (header.Type == "RUN_FINISHED" || header.Type == "RUN_ERROR")
				return AGUIEvent{ID: id, Type: header.Type, Data: json.RawMessage(raw)}, nil
			}
			if s.eof {
				return AGUIEvent{}, s.observationError(io.ErrUnexpectedEOF)
			}
			continue
		}
		appendAGUILine(line, &data, &id)
	}
}
func appendAGUILine(line string, data *strings.Builder, id *string) {
	if strings.HasPrefix(line, ":") {
		return
	}
	field, value, found := strings.Cut(line, ":")
	if !found {
		value = ""
	}
	value = strings.TrimPrefix(value, " ")
	switch field {
	case "data":
		data.WriteString(value)
		data.WriteByte('\n')
	case "id":
		if !strings.ContainsRune(value, '\x00') {
			*id = value
		}
	}
}

// RunAGUI submits exactly one immutable run envelope through the existing
// authenticated HTTP client. It never retries or switches transport implicitly.
func (c *HTTPClient) RunAGUI(ctx context.Context, input *agui.RunAgentInput, options *AGUIRunOptions) (*AGUIRunStream, error) {
	if c == nil || input == nil {
		return nil, fmt.Errorf("AG-UI input is required")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if err = agui.ValidateInput(body); err != nil {
		return nil, fmt.Errorf("invalid AG-UI input: %w", err)
	}
	request, err := c.newRequest(ctx, http.MethodPost, "/v1/ag-ui/run", bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if options != nil && options.AfterEventID != "" {
		request.Header.Set("Last-Event-ID", options.AfterEventID)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, &AGUIObservationError{ThreadID: input.ThreadID, RunID: input.RunID, Cause: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 32<<10))
		return nil, fmt.Errorf("AG-UI request failed: %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		response.Body.Close()
		return nil, &AGUIObservationError{ThreadID: input.ThreadID, RunID: input.RunID, Cause: fmt.Errorf("expected AG-UI event stream")}
	}
	return &AGUIRunStream{ThreadID: input.ThreadID, RunID: input.RunID, body: response.Body, reader: bufio.NewReader(response.Body)}, nil
}
func (c *HTTPClient) AttachAGUI(ctx context.Context, threadID, runID, afterEventID string) (*AGUIRunStream, error) {
	forwarded, _ := json.Marshal(map[string]any{"agently": map[string]any{"version": "1", "operation": "run.attach", "requestId": runID, "target": map[string]string{"threadId": threadID}, "payload": map[string]any{}}})
	return c.RunAGUI(ctx, &agui.RunAgentInput{ThreadID: threadID, RunID: runID, Messages: []agui.Message{}, ForwardedProps: forwarded}, &AGUIRunOptions{AfterEventID: afterEventID})
}
