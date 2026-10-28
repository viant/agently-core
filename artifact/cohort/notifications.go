package cohort

import (
	"context"
	"log"
	"net/http"
	"os"
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

// startArtifactCohortNotifications keeps a notification-only legacy MCP
// session. Artifact reads continue on the independent stateless client. A
// failed session is replaced and re-admitted by artifact/discover; the normal
// conditional replica poll remains the recovery path while this is offline.
func startArtifactCohortNotifications(parent context.Context, endpoint, origin string, token HostToken, knownHash func() string, handler *artifactcohort.NotificationHandler) func() {
	return startArtifactCohortNotificationsWithInterval(parent, endpoint, origin, token, knownHash, handler, 5*time.Second)
}

func startArtifactCohortNotificationsWithInterval(parent context.Context, endpoint, origin string, token HostToken, knownHash func() string, handler *artifactcohort.NotificationHandler, pingInterval time.Duration) func() {
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
			client, err := openArtifactCohortNotificationSession(ctx, endpoint, origin, token, hash, handler)
			if err == nil {
				// A ping is a read-only session health check. It detects a producer
				// restart even when its old SSE stream has not reported closure.
				ticker := time.NewTicker(pingInterval)
				for err == nil {
					select {
					case <-ctx.Done():
						err = ctx.Err()
					case <-ticker.C:
						pingCtx, stop := context.WithTimeout(ctx, 3*time.Second)
						_, err = client.Ping(pingCtx, &schema.PingRequestParams{}, mcpclient.WithNoRetry())
						stop()
					}
				}
				ticker.Stop()
				client.Close()
			}
			if ctx.Err() != nil {
				return
			}
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

func openArtifactCohortNotificationSession(parent context.Context, endpoint, origin string, token HostToken, knownHash string, handler *artifactcohort.NotificationHandler) (*mcpclient.Client, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	openTiming := observe.Start(ctx, "artifact.notification.session.open", observe.Labels{})
	transport, err := streamable.New(ctx, endpoint,
		streamable.WithHTTPClient(&http.Client{Transport: artifactHostBearer{base: http.DefaultTransport, token: token, origin: origin}}),
		streamable.WithHandler(mcpclient.NewHandler(handler)),
		streamable.WithProtocolVersion(schema.LegacyProtocolVersion),
		streamable.WithRequestHeaderProvider(artifactRequestHeaders))
	if err != nil {
		openTiming.End(observe.OutcomeFor(err), observe.CacheBypass)
		return nil, err
	}
	client := mcpclient.New("agently-artifact-notifications", "1", transport, mcpclient.WithProtocolVersion(schema.LegacyProtocolVersion))
	_, err = client.Initialize(ctx)
	openTiming.End(observe.OutcomeFor(err), observe.CacheBypass)
	if err == nil {
		subscribeTiming := observe.Start(ctx, "artifact.notification.subscribe", observe.Labels{})
		var manifest artifactcohort.Manifest
		manifest, err = (artifactcohort.MCPCaller{Client: client}).DiscoverKnown(ctx, knownHash)
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
		return nil, err
	}
	return client, nil
}
