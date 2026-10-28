package cohort

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/viant/agently-core/protocol/primitive/observe"
	"github.com/viant/forge/backend/artifactcohort"
	"github.com/viant/jsonrpc/transport/client/http/streamable"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

func artifactNotificationTraceContext(ctx context.Context) context.Context {
	if os.Getenv("BACKEND_PRIMITIVE_TRACE") != "1" {
		return ctx
	}
	return observe.WithObserver(ctx, observe.ObserverFunc(func(event observe.Event) {
		// Phases and outcomes are fixed enums; no URI, source, principal, token,
		// payload, or raw error enters this diagnostic line.
		log.Printf("primitive cohort push phase=%s at_unix_ms=%d duration_ms=%.3f calls=1 bytes=%d outcome=%s cache=%s", event.Phase, time.Now().UnixMilli(), event.Duration.Seconds()*1000, event.Bytes, event.Outcome, event.Cache)
	}))
}

// StartNotifications starts a notification-only stateful MCP session for an
// explicitly configured namespace. Artifact reads stay on the independent
// stateless client. Lost streams fence the replica and queue validated Sync in
// the handler's background retry loop; request handling never reconciles MCP.
// Endpoint, namespace and credential must come from trusted host registration.
func StartNotifications(parent context.Context, endpoint, origin, namespace string, token HostToken, knownHash func() string, handler *artifactcohort.NotificationHandler) (func(), error) {
	if parent == nil || parent.Err() != nil || strings.TrimSpace(endpoint) == "" || namespace == "" || strings.TrimSpace(namespace) != namespace || strings.ContainsAny(namespace, "/:?#") || token == nil || knownHash == nil || handler == nil {
		return nil, artifactcohort.ErrInvalid
	}
	return startArtifactCohortNotificationsWithInterval(parent, endpoint, origin, namespace, token, knownHash, handler, 5*time.Second), nil
}

func startArtifactCohortNotificationsWithInterval(parent context.Context, endpoint, origin, namespace string, token HostToken, knownHash func() string, handler *artifactcohort.NotificationHandler, pingInterval time.Duration) func() {
	ctx, cancel := context.WithCancel(artifactNotificationTraceContext(parent))
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		for ctx.Err() == nil {
			// The authoritative stateless preload runs immediately after Start.
			// Wait for its activated hash so the notification subscription can
			// use a compact unchanged reply instead of downloading the catalog.
			hash := ""
			if knownHash != nil {
				hash = knownHash()
			}
			if hash == "" {
				select {
				case <-ctx.Done():
					return
				case <-time.After(25 * time.Millisecond):
				}
				continue
			}
			sessionCtx, closeSession := context.WithCancel(ctx)
			client, lost, err := openArtifactCohortNotificationSession(sessionCtx, endpoint, origin, namespace, token, hash, handler)
			if err == nil {
				// A ping is a read-only session health check. It detects a producer
				// restart even when its old SSE stream has not reported closure.
				ticker := time.NewTicker(pingInterval)
				for err == nil {
					select {
					case <-ctx.Done():
						err = ctx.Err()
					case <-lost:
						err = fmt.Errorf("artifact notification stream disconnected")
					case <-ticker.C:
						pingCtx, stop := context.WithTimeout(ctx, 3*time.Second)
						_, err = client.Ping(pingCtx, &schema.PingRequestParams{}, mcpclient.WithNoRetry())
						stop()
					}
				}
				ticker.Stop()
				closeSession()
				client.Close()
			}
			closeSession()
			if ctx.Err() != nil {
				return
			}
			// The handler marks stale synchronously and retries the stateless
			// conditional sync in its own worker while the sidecar reconnects.
			handler.ReconcileHint()
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return func() {
		cancel()
		done.Wait()
	}
}

func openArtifactCohortNotificationSession(parent context.Context, endpoint, origin, namespace string, token HostToken, knownHash string, handler *artifactcohort.NotificationHandler) (*mcpclient.Client, <-chan struct{}, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	openTiming := observe.Start(ctx, "artifact.notification.session.open", observe.Labels{})
	lost := make(chan struct{}, 1)
	onLost := func() {
		if parent.Err() == nil {
			select {
			case lost <- struct{}{}:
			default:
			}
		}
	}
	transport, err := streamable.New(ctx, endpoint,
		streamable.WithHTTPClient(&http.Client{Transport: notificationStreamTransport{base: artifactHostBearer{base: http.DefaultTransport, token: token, origin: origin}, lost: onLost}}),
		streamable.WithHandler(mcpclient.NewHandler(handler)),
		streamable.WithProtocolVersion(schema.LegacyProtocolVersion),
		streamable.WithRequestHeaderProvider(artifactRequestHeaders))
	if err != nil {
		openTiming.End(observe.OutcomeFor(err), observe.CacheBypass)
		return nil, nil, err
	}
	client := mcpclient.New("agently-artifact-notifications", "1", transport, mcpclient.WithProtocolVersion(schema.LegacyProtocolVersion))
	_, err = client.Initialize(ctx)
	openTiming.End(observe.OutcomeFor(err), observe.CacheBypass)
	if err == nil {
		subscribeTiming := observe.Start(ctx, "artifact.notification.subscribe", observe.Labels{})
		var manifest artifactcohort.Manifest
		manifest, err = (artifactcohort.NamespaceCaller{Client: client, Namespace: namespace}).DiscoverKnown(ctx, knownHash)
		cache := observe.CacheUnknown
		if err == nil && manifest.Unchanged {
			cache = observe.CacheHit
		} else if err == nil {
			cache = observe.CacheMiss
		}
		subscribeTiming.End(observe.OutcomeFor(err), cache)
		if err == nil && !manifest.Unchanged && manifest.Hash != knownHash {
			handler.ReconcileHint()
		}
	}
	if err != nil {
		client.Close()
		return nil, nil, err
	}
	return client, lost, nil
}

type notificationStreamTransport struct {
	base http.RoundTripper
	lost func()
}

func (t notificationStreamTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if request.Method != http.MethodGet {
		return response, err
	}
	if err != nil || response.StatusCode != http.StatusOK {
		t.lost()
		return response, err
	}
	response.Body = notificationStreamBody{ReadCloser: response.Body, lost: t.lost}
	return response, nil
}

type notificationStreamBody struct {
	io.ReadCloser
	lost func()
}

func (b notificationStreamBody) Read(data []byte) (int, error) {
	n, err := b.ReadCloser.Read(data)
	if err != nil {
		b.lost()
	}
	return n, err
}
