package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

const overloadedErrorJSON = `{"error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later.","param":null}}`

func TestClient_Generate_UsesRetryAfterFor503(t *testing.T) {
	testCases := []struct {
		name                string
		contextContinuation bool
		wantPath            string
	}{
		{name: "Responses API", contextContinuation: true, wantPath: "/responses"},
		{name: "chat completions API", contextContinuation: false, wantPath: "/chat/completions"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			client := NewClient("test", "gpt-test", WithContextContinuation(&testCase.contextContinuation))
			client.BaseURL = "http://openai.test"
			client.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, testCase.wantPath, req.URL.Path)
				return overloadHTTPResponse(req, "7"), nil
			})}

			_, err := client.Generate(context.Background(), &llm.GenerateRequest{
				Messages: []llm.Message{llm.NewUserMessage("hello")},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "status 503")

			delay, retry := client.AdviseBackoff(err, 0)
			assert.True(t, retry)
			assert.Equal(t, 7*time.Second, delay)
		})
	}
}

func TestClient_Stream_UsesRetryAfterFor503(t *testing.T) {
	client := NewClient("test", "gpt-test")
	client.BaseURL = "http://openai.test"
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return overloadHTTPResponse(req, "0"), nil
	})}

	events, err := client.Stream(context.Background(), &llm.GenerateRequest{
		Messages: []llm.Message{llm.NewUserMessage("hello")},
	})
	require.NoError(t, err)

	var streamErr error
	for event := range events {
		if event.Err != nil {
			streamErr = event.Err
			break
		}
	}
	require.Error(t, streamErr)
	delay, retry := client.AdviseBackoff(streamErr, 0)
	assert.True(t, retry)
	assert.Zero(t, delay)
}

func TestClient_AdviseBackoff_DoesNotOverrideOtherErrorsOrMissingHeader(t *testing.T) {
	client := &Client{}
	baseErr := errors.New("OpenAI API error (status 502): bad gateway")
	assert.Same(t, baseErr, withOpenAIRetryAfter(baseErr, http.StatusBadGateway, http.Header{"Retry-After": []string{"9"}}))

	missingHeaderErr := withOpenAIRetryAfter(errors.New("OpenAI API error (status 503): unavailable"), http.StatusServiceUnavailable, nil)
	delay, retry := client.AdviseBackoff(missingHeaderErr, 0)
	assert.False(t, retry)
	assert.Zero(t, delay)
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, time.September, 9, 16, 0, 0, 0, time.UTC)
	testCases := []struct {
		name      string
		value     string
		wantDelay time.Duration
		wantOK    bool
	}{
		{name: "delta seconds", value: "12", wantDelay: 12 * time.Second, wantOK: true},
		{name: "zero delta seconds", value: "0", wantDelay: 0, wantOK: true},
		{name: "HTTP date", value: now.Add(9 * time.Second).Format(http.TimeFormat), wantDelay: 9 * time.Second, wantOK: true},
		{name: "past HTTP date", value: now.Add(-time.Second).Format(http.TimeFormat), wantOK: false},
		{name: "invalid", value: "later", wantOK: false},
		{name: "negative", value: "-1", wantOK: false},
		{name: "overflow", value: "9223372036854775807", wantOK: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			delay, ok := parseRetryAfter(testCase.value, now)
			assert.Equal(t, testCase.wantOK, ok)
			assert.Equal(t, testCase.wantDelay, delay)
		})
	}
}

func overloadHTTPResponse(req *http.Request, retryAfter string) *http.Response {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	header.Set("Retry-After", retryAfter)
	return &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(overloadedErrorJSON)),
		Request:    req,
	}
}
