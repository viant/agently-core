package sdk

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

func newHandlerHTTPClient(t interface {
	Helper()
	Fatalf(string, ...interface{})
}, handler http.Handler) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Result(), nil
	})}
}

func newHandlerBackedHTTP(t interface {
	Helper()
	Fatalf(string, ...interface{})
}, handler http.Handler) *HTTPClient {
	t.Helper()
	client, err := NewHTTP("https://sdk.example.test", WithHTTPClient(newHandlerHTTPClient(t, handler)))
	if err != nil {
		t.Fatalf("NewHTTP() error = %v", err)
	}
	return client
}

func newStreamingHandlerBackedHTTP(t interface {
	Helper()
	Fatalf(string, ...interface{})
}, handler http.Handler) *HTTPClient {
	t.Helper()
	client, err := NewHTTP("https://sdk.example.test", WithHTTPClient(newStreamingHandlerHTTPClient(t, handler)))
	if err != nil {
		t.Fatalf("NewHTTP() error = %v", err)
	}
	return client
}

func newStreamingHandlerHTTPClient(t interface {
	Helper()
	Fatalf(string, ...interface{})
}, handler http.Handler) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		pr, pw := io.Pipe()
		writer := &streamingResponseWriter{
			header: make(http.Header),
			status: http.StatusOK,
			body:   pw,
			ready:  make(chan struct{}),
		}
		go func() {
			defer pw.Close()
			handler.ServeHTTP(writer, req)
			writer.signalReady()
		}()
		<-writer.ready
		return &http.Response{
			StatusCode: writer.status,
			Status:     http.StatusText(writer.status),
			Header:     writer.header.Clone(),
			Body:       pr,
			Request:    req,
		}, nil
	})}
}

type streamingResponseWriter struct {
	header http.Header
	status int
	body   *io.PipeWriter

	once  sync.Once
	ready chan struct{}
}

func (w *streamingResponseWriter) Header() http.Header {
	return w.header
}

func (w *streamingResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.signalReady()
}

func (w *streamingResponseWriter) Write(data []byte) (int, error) {
	w.signalReady()
	return w.body.Write(data)
}

func (w *streamingResponseWriter) Flush() {
	w.signalReady()
}

func (w *streamingResponseWriter) signalReady() {
	w.once.Do(func() {
		close(w.ready)
	})
}
