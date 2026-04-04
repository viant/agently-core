package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClient_UploadFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if r.URL.Path != "/v1/files" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if got := r.FormValue("conversationId"); got != "conv_1" {
			t.Fatalf("unexpected conversationId %q", got)
		}
		if got := r.FormValue("name"); got != "note.txt" {
			t.Fatalf("unexpected name %q", got)
		}
		if got := r.FormValue("contentType"); got != "text/plain" {
			t.Fatalf("unexpected contentType %q", got)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		defer file.Close()
		if header.Filename != "note.txt" {
			t.Fatalf("unexpected filename %q", header.Filename)
		}
		var data bytes.Buffer
		if _, err := data.ReadFrom(file); err != nil {
			t.Fatalf("read uploaded file: %v", err)
		}
		if got := data.String(); got != "hello" {
			t.Fatalf("unexpected file body %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&UploadFileOutput{ID: "file_1", URI: "/v1/files/file_1?conversationId=conv_1"})
	}))
	defer srv.Close()

	client, err := NewHTTP(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	out, err := client.UploadFile(context.Background(), &UploadFileInput{
		ConversationID: "conv_1",
		Name:           "note.txt",
		ContentType:    "text/plain",
		Data:           []byte("hello"),
	})
	if err != nil {
		t.Fatalf("upload file: %v", err)
	}
	if out == nil || out.ID != "file_1" {
		t.Fatalf("unexpected output %+v", out)
	}
}

type spyUploadClient struct {
	*HTTPClient
	input *UploadFileInput
	out   *UploadFileOutput
	err   error
}

func (s *spyUploadClient) UploadFile(_ context.Context, input *UploadFileInput) (*UploadFileOutput, error) {
	s.input = input
	if s.err != nil {
		return nil, s.err
	}
	if s.out != nil {
		return s.out, nil
	}
	return &UploadFileOutput{ID: "file_1"}, nil
}

func TestHandler_UploadFile(t *testing.T) {
	base, err := NewHTTP("http://example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	spy := &spyUploadClient{HTTPClient: base}
	handler := NewHandler(spy)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("conversationId", "conv_1"); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("contentType", "text/plain"); err != nil {
		t.Fatal(err)
	}
	part, err := w.CreateFormFile("file", "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/files", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body=%s", rec.Code, rec.Body.String())
	}
	if spy.input == nil {
		t.Fatalf("upload input was not captured")
	}
	if spy.input.ConversationID != "conv_1" {
		t.Fatalf("unexpected conversation ID %q", spy.input.ConversationID)
	}
	if spy.input.Name != "note.txt" {
		t.Fatalf("unexpected file name %q", spy.input.Name)
	}
	if spy.input.ContentType != "text/plain" {
		t.Fatalf("unexpected content type %q", spy.input.ContentType)
	}
	if string(spy.input.Data) != "hello" {
		t.Fatalf("unexpected data %q", string(spy.input.Data))
	}

	var out UploadFileOutput
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.ID != "file_1" {
		t.Fatalf("unexpected output %+v", out)
	}
	if out.URI == "" {
		t.Fatalf("expected synthesized URI")
	}
}
