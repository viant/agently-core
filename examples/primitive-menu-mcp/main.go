// Command primitive-menu-mcp is a standalone protected MCP menu producer.
// Run: MENU_EXAMPLE_TOKEN=<opaque secret> go run ./examples/primitive-menu-mcp
// Production hosts inject their IdP verification and resource revision policy.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	identity "github.com/viant/agently-core/protocol/resource"
	producer "github.com/viant/agently-core/service/primitiveproducer"
	"log"
	"net/http"
	"os"
	"time"
)

type actorKey struct{}
type policy struct{ actor identity.VerifiedActor }

func (p policy) SelectRevision(_ context.Context, _ identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if len(candidates) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: "example-configured-account", ValidUntil: p.actor.ValidUntil}, nil
}
func main() {
	token := os.Getenv("MENU_EXAMPLE_TOKEN")
	if len(token) < 16 {
		log.Fatal("set MENU_EXAMPLE_TOKEN to an opaque secret of at least 16 characters")
	}
	uri, _ := identity.ParseResourceURI("menu://platform/navigation")
	source := &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) {
		return json.RawMessage(`{"schemaVersion":1,"applications":[{"id":"platform","title":"Platform","menus":[{"id":"windows","title":"Windows","action":{"type":"window","windowKey":"windows"}},{"id":"datasources","title":"Data sources","action":{"type":"window","windowKey":"datasources"}},{"id":"reports","title":"Reports","action":{"type":"window","windowKey":"reports"}}]}]}`), nil
	}}
	p, err := producer.NewLocalProvider(producer.LocalConfig{ProviderIdentity: "standalone-example", Actor: func(ctx context.Context) (identity.VerifiedActor, error) {
		a, ok := ctx.Value(actorKey{}).(identity.VerifiedActor)
		if !ok {
			return a, identity.ErrResourceDenied
		}
		return a, nil
	}, Verify: func(ctx context.Context, a identity.VerifiedActor) error {
		if current, ok := ctx.Value(actorKey{}).(identity.VerifiedActor); !ok || current != a {
			return identity.ErrResourceDenied
		}
		return nil
	}, Authorize: func(_ context.Context, _ identity.VerifiedActor, u identity.ResourceURI, _ string) error {
		if u.Namespace != "platform" {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: []producer.LocalResourceBinding{{URI: uri.String(), Title: "Platform", FormatVersion: 1, Resolver: func(_ context.Context, a identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
		return &identity.ResourceResolver{ProviderIdentity: "standalone-example", Source: source, Policy: policy{actor: a}}, nil
	}}}, Validators: map[string]producer.LocalResourceValidator{"menu": producer.ValidateMenu}, MenuActionVisibility: func(_ context.Context, _ identity.VerifiedActor, _ identity.ResourceURI, raw json.RawMessage) (bool, error) {
		var action struct {
			Type      string `json:"type"`
			WindowKey string `json:"windowKey"`
		}
		if json.Unmarshal(raw, &action) != nil {
			return false, identity.ErrResourceDenied
		}
		return action.Type == "window" && (action.WindowKey == "windows" || action.WindowKey == "datasources" || action.WindowKey == "reports"), nil
	}})
	if err != nil {
		log.Fatal(err)
	}
	handler, err := producer.NewMCPHandler(p, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			actor := identity.VerifiedActor{Subject: "example-reader", Issuer: "example-configured-issuer", TenantID: "example", AccountID: "example-account", IdentityRevision: "configuration-1", ValidUntil: time.Now().Add(time.Minute)}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, actor)))
		})
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Print("standalone menu MCP listening at http://127.0.0.1:29395/mcp")
	log.Fatal((&http.Server{Addr: "127.0.0.1:29395", Handler: handler, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe())
}
