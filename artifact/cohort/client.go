package cohort

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/viant/agently-core/app/executor"
	"github.com/viant/forge/backend/artifactcohort"
	"github.com/viant/jsonrpc/transport/client/http/streamable"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

// HostToken supplies a producer-authorized host credential from an
// operator-owned service identity. It is never called with a browser request
// or Agently user token. A rotating implementation can refresh on each call.
type HostToken func(context.Context) (string, error)
type artifactHostBearer struct {
	base   http.RoundTripper
	token  HostToken
	origin string
}

func (b artifactHostBearer) RoundTrip(request *http.Request) (*http.Response, error) {
	token, err := b.token(request.Context())
	if err != nil || strings.TrimSpace(token) == "" {
		return nil, artifactcohort.ErrDenied
	}
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	if b.origin != "" {
		clone.Header.Set("Origin", b.origin)
	}
	return b.base.RoundTrip(clone)
}

// NewHostClient opens a dedicated stateless MCP client for the
// polled native catalog. A producer restart cannot strand a negotiated
// Mcp-Session-Id in this client. expectedEndpoint pins the trusted destination.
func NewHostClient(ctx context.Context, rt *executor.Runtime, connection, provider, expectedEndpoint, origin string, token HostToken) (mcpclient.Interface, *artifactcohort.NotificationHandler, error) {
	if ctx == nil || ctx.Err() != nil || rt == nil || rt.MCPManager == nil || connection == "" || provider == "" || token == nil {
		return nil, nil, artifactcohort.ErrInvalid
	}
	options, err := rt.MCPManager.Options(ctx, connection)
	if err != nil || options == nil || options.ClientOptions == nil || options.PrimitiveProviderIdentity != provider || options.ClientOptions.Transport.Type != "streamable" {
		return nil, nil, artifactcohort.ErrDenied
	}
	endpoint := options.ClientOptions.Transport.URL
	parsed, e := url.Parse(endpoint)
	if e != nil || parsed.Host == "" || parsed.Path == "" || endpoint != expectedEndpoint || parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost")) {
		return nil, nil, artifactcohort.ErrDenied
	}
	handler := artifactcohort.NewNotificationHandler(artifactNotificationTraceContext(ctx), nil)
	transport, err := streamable.New(ctx, endpoint,
		streamable.WithHTTPClient(&http.Client{Transport: artifactHostBearer{base: http.DefaultTransport, token: token, origin: origin}}),
		streamable.WithHandler(mcpclient.NewHandler(handler)),
		streamable.WithProtocolVersion(schema.LatestProtocolVersion),
		streamable.WithStateless(),
		streamable.WithRequestHeaderProvider(artifactRequestHeaders))
	if err != nil {
		handler.Close()
		return nil, nil, fmt.Errorf("artifact cohort transport: %w", err)
	}
	client := mcpclient.New("agently-artifact-host", "1", transport, mcpclient.WithProtocolVersion(schema.LatestProtocolVersion))
	if _, err = client.Initialize(ctx); err != nil {
		client.Close()
		handler.Close()
		return nil, nil, fmt.Errorf("artifact cohort initialize: %w", err)
	}
	return client, handler, nil
}

func artifactRequestHeaders(_ context.Context, body []byte, headers http.Header) error {
	var request struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if json.Unmarshal(body, &request) != nil || request.Method == "" {
		return errors.New("invalid MCP request")
	}
	headers.Set(schema.HeaderMethod, request.Method)
	if request.Method == schema.MethodToolsCall {
		headers.Set(schema.HeaderName, request.Params.Name)
	}
	return nil
}
