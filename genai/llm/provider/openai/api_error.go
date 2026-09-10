package openai

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// openAIRetryAfterError keeps OpenAI's Retry-After value attached to the
// existing error without changing its text or the behavior of other errors.
type openAIRetryAfterError struct {
	err        error
	retryAfter time.Duration
	hasDelay   bool
}

func (e *openAIRetryAfterError) Error() string {
	return e.err.Error()
}

func (e *openAIRetryAfterError) Unwrap() error {
	return e.err
}

func withOpenAIRetryAfter(err error, statusCode int, header http.Header) error {
	if err == nil || statusCode != http.StatusServiceUnavailable {
		return err
	}
	delay, ok := parseRetryAfter(header.Get("Retry-After"), time.Now())
	return &openAIRetryAfterError{err: err, retryAfter: delay, hasDelay: ok}
}

// AdviseBackoff only overrides the existing retry delay when OpenAI supplied a
// valid Retry-After value. Otherwise core falls back to its existing policy.
func (c *Client) AdviseBackoff(err error, _ int) (time.Duration, bool) {
	var retryErr *openAIRetryAfterError
	if errors.As(err, &retryErr) && retryErr.hasDelay {
		return retryErr.retryAfter, true
	}
	return 0, false
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 || seconds > int64(time.Duration(1<<63-1)/time.Second) {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := retryAt.Sub(now)
	if delay <= 0 {
		return 0, false
	}
	return delay, true
}
