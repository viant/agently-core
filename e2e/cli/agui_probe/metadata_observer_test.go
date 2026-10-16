package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/viant/agently-core/sdk"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type metadataTransport func(*http.Request) (*http.Response, error)

func (f metadataTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedMetadataBody struct {
	io.Reader
	reads, closes int
	closeErr      error
}

func (b *trackedMetadataBody) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (b *trackedMetadataBody) Close() error               { b.closes++; return b.closeErr }

func TestMetadataObserverPreservesConsumptionCloseAndFixedFields(t *testing.T) {
	for _, tc := range []struct {
		name, text, shape string
		fields            int
		present, match    bool
	}{
		{"valid", `{"id":"owned-sensitive-id","unknown-secret-key":"secret-marker"}`, "object", 2, true, true},
		{"null", `null`, "null", 0, false, false},
		{"missing", `{"unknown-secret-key":"secret-marker"}`, "object", 1, false, false},
		{"foreign", `{"id":"foreign-sensitive-id"}`, "object", 1, true, false},
		{"malformed", `{"id":`, "malformed", 0, false, false},
		{"other", `["secret-marker"]`, "other", 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closeErr := errors.New("original close sentinel")
			original := &trackedMetadataBody{Reader: strings.NewReader(tc.text), closeErr: closeErr}
			o := &observer{next: metadataTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: original}, nil
			})}
			request, _ := http.NewRequest("GET", "http://fixture/v1/conversations/owned-sensitive-id?includeTranscript=false", nil)
			request.Header.Set("Authorization", "Bearer token-marker")
			response, err := o.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			if original.reads != 0 || o.observed()[0].Metadata != nil {
				t.Fatal("eager read or premature diagnostic")
			}
			bytes, err := io.ReadAll(response.Body)
			if err != nil || string(bytes) != tc.text {
				t.Fatal("response bytes changed")
			}
			if err = response.Body.Close(); err != closeErr || original.closes != 1 {
				t.Fatal("original close/error changed")
			}
			d := o.observed()[0].Metadata
			if d == nil || d.Shape != tc.shape || d.FieldCount != tc.fields || d.ReturnedIDPresent != tc.present || d.ReturnedIDMatchesRequest != tc.match {
				t.Fatal("wrong safe classification")
			}
			if d.RequestedIDHash != digest("owned-sensitive-id") {
				t.Fatal("request identity hash lost")
			}
			encoded, _ := json.Marshal(o.observed())
			for _, secret := range []string{"owned-sensitive-id", "foreign-sensitive-id", "unknown-secret-key", "secret-marker", "token-marker", "Authorization"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatal("raw value leaked")
				}
			}
		})
	}
}
func TestMetadataObserverOversizeCaptureIsBoundedAndDoesNotTruncate(t *testing.T) {
	text := `{"id":"owned","unknown":"` + strings.Repeat("secret", maxMetadataCaptureBytes) + `"}`
	original := &trackedMetadataBody{Reader: strings.NewReader(text)}
	var observed conversationMetadataObservation
	body := &metadataCaptureBody{original: original, requestedID: "owned", publish: func(d conversationMetadataObservation) { observed = d }}
	bytes, err := io.ReadAll(body)
	if err != nil || string(bytes) != text {
		t.Fatal("oversize response truncated")
	}
	if len(body.capture) > maxMetadataCaptureBytes {
		t.Fatal("unbounded capture")
	}
	reads := original.reads
	if body.Close() != nil || original.reads != reads || original.closes != 1 {
		t.Fatal("close reread/drain or original close changed")
	}
	if observed.Shape != "oversize" || observed.ReturnedIDHash != "" || observed.FieldCount != 0 || body.capture != nil {
		t.Fatal("oversize leaked content or retained capture")
	}
}
func TestMetadataObserverDoesNotDrainPartialReadOrWrapOtherRoutes(t *testing.T) {
	original := &trackedMetadataBody{Reader: strings.NewReader(`{"id":"owned"}`)}
	var partial conversationMetadataObservation
	body := &metadataCaptureBody{original: original, requestedID: "owned", publish: func(d conversationMetadataObservation) { partial = d }}
	bytes := make([]byte, 1)
	body.Read(bytes)
	reads := original.reads
	body.Close()
	if original.reads != reads || original.closes != 1 {
		t.Fatal("close changed partial consumption")
	}
	if partial.Shape != "incomplete" {
		t.Fatal("partial consumption mislabeled entire response malformed")
	}
	for _, route := range []struct{ method, path string }{{"POST", "/v1/conversations/owned?includeTranscript=false"}, {"GET", "/v1/conversations/owned/transcript?includeTranscript=false"}, {"GET", "/v1/conversations/owned"}, {"GET", "/v1/api/payload/owned?includeTranscript=false"}, {"GET", "/v1/ag-ui/run?includeTranscript=false"}, {"GET", "/v1/conversations/owned?includeTranscript=false&extra=1"}} {
		original := &trackedMetadataBody{Reader: strings.NewReader("fixture")}
		o := &observer{next: metadataTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: original}, nil
		})}
		r, _ := http.NewRequest(route.method, "http://fixture"+route.path, nil)
		response, _ := o.RoundTrip(r)
		if response.Body != original {
			t.Fatal("unrelated route wrapped")
		}
	}
}
func TestMetadataObserverSDKStrictIdentityRemainsEnforced(t *testing.T) {
	for _, text := range []string{`{"id":"owned"}`, `null`, `{}`, `{"id":"foreign"}`} {
		o := &observer{next: metadataTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(text))}, nil
		})}
		client, err := sdk.NewHTTP("http://fixture", sdk.WithHTTPClient(&http.Client{Transport: o}))
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.PrepareAGUIChat(context.Background(), &agentsvc.QueryInput{ConversationID: "owned", AgentID: "fixture", Query: "fixture"})
		if (err == nil) != (text == `{"id":"owned"}`) {
			t.Fatal("strict SDK ID check changed")
		}
		if err != nil && result != nil {
			t.Fatal("denied result exposed")
		}
		if len(o.observed()) != 1 || o.observed()[0].Metadata == nil {
			t.Fatal("diagnostic not finalized after SDK close")
		}
	}
}

type finalMetadataReader struct {
	data []byte
	done bool
}

func (r *finalMetadataReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), io.EOF
}
func (r *finalMetadataReader) Close() error { return nil }
func TestMetadataObserverEOFWithBytesConfirmsMalformed(t *testing.T) {
	var result conversationMetadataObservation
	body := &metadataCaptureBody{original: &finalMetadataReader{data: []byte(`{"id":`)}, requestedID: "owned", publish: func(d conversationMetadataObservation) { result = d }}
	buffer := make([]byte, 100)
	n, err := body.Read(buffer)
	if n == 0 || err != io.EOF {
		t.Fatal("original EOF changed")
	}
	body.Close()
	if result.Shape != "malformed" {
		t.Fatal("observed EOF lost")
	}
}
func TestMetadataObserverConcurrentSnapshots(t *testing.T) {
	original := &trackedMetadataBody{Reader: strings.NewReader(`{"id":"owned","unknown":"` + strings.Repeat("fixture", 1000) + `"}`)}
	o := &observer{next: metadataTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: original}, nil
	})}
	request, _ := http.NewRequest("GET", "http://fixture/v1/conversations/owned?includeTranscript=false", nil)
	response, _ := o.RoundTrip(request)
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, response.Body); response.Body.Close(); close(done) }()
	for {
		select {
		case <-done:
			if o.observed()[0].Metadata == nil {
				t.Fatal("missing completed diagnostic")
			}
			return
		default:
			json.Marshal(o.observed())
		}
	}
}
