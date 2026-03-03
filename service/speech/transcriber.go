package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

// Transcriber converts audio to text.
type Transcriber interface {
	Transcribe(ctx context.Context, filename string, audio io.Reader) (string, error)
}

// OpenAITranscriber uses the OpenAI Whisper API for speech transcription.
type OpenAITranscriber struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// NewOpenAITranscriber creates a transcriber backed by the OpenAI Whisper API.
func NewOpenAITranscriber(apiKey string, opts ...TranscriberOption) *OpenAITranscriber {
	t := &OpenAITranscriber{
		apiKey:  apiKey,
		baseURL: "https://api.openai.com/v1",
		model:   "whisper-1",
		client:  http.DefaultClient,
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// TranscriberOption customises the OpenAI transcriber.
type TranscriberOption func(*OpenAITranscriber)

// WithTranscriberBaseURL overrides the OpenAI API base URL.
func WithTranscriberBaseURL(u string) TranscriberOption {
	return func(t *OpenAITranscriber) { t.baseURL = u }
}

// WithTranscriberModel overrides the Whisper model name.
func WithTranscriberModel(m string) TranscriberOption {
	return func(t *OpenAITranscriber) { t.model = m }
}

// WithTranscriberHTTPClient overrides the HTTP client.
func WithTranscriberHTTPClient(c *http.Client) TranscriberOption {
	return func(t *OpenAITranscriber) { t.client = c }
}

// Transcribe sends audio to the OpenAI Whisper API and returns the transcribed text.
func (t *OpenAITranscriber) Transcribe(ctx context.Context, filename string, audio io.Reader) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("model", t.model); err != nil {
		return "", fmt.Errorf("write model field: %w", err)
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, audio); err != nil {
		return "", fmt.Errorf("copy audio: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.baseURL+"/audio/transcriptions", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("whisper API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode whisper response: %w", err)
	}
	return result.Text, nil
}
