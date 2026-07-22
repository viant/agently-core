package ollama

import (
	"net/http"
	"time"

	basecfg "github.com/viant/agently-core/genai/llm/provider/base"
)

type ClientOption func(*Client)

func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) { basecfg.WithBaseURL(baseURL)(&c.Config) }
}

func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) { basecfg.WithHTTPClient(client)(&c.Config) }
}

func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) { basecfg.WithTimeout(timeout)(&c.Config) }
}

func WithModel(model string) ClientOption {
	return func(c *Client) { basecfg.WithModel(model)(&c.Config) }
}

// WithMaxTokens sets a default output limit for requests that do not specify one.
func WithMaxTokens(max int) ClientOption {
	return func(c *Client) { c.MaxTokens = max }
}

// WithKeepAlive keeps the loaded Ollama model resident for the requested
// duration after each generation, avoiding an expensive reload on the next
// interactive turn.
func WithKeepAlive(duration string) ClientOption {
	return func(c *Client) { c.KeepAlive = duration }
}

// WithContextWindow sets Ollama's num_ctx option for each request.
func WithContextWindow(tokens int) ClientOption {
	return func(c *Client) { c.ContextWindow = tokens }
}

// WithTemperature sets a default sampling temperature for requests that do not specify one.
func WithTemperature(temperature float64) ClientOption {
	return func(c *Client) { c.Temperature = &temperature }
}

// WithUsageListener registers a callback to receive token usage information.
func WithUsageListener(l basecfg.UsageListener) ClientOption {
	return func(c *Client) { c.UsageListener = l }
}
