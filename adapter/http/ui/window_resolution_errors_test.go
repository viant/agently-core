package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/viant/afs"
	identity "github.com/viant/agently-core/protocol/resource"
	service "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	"github.com/viant/forge/backend/service/meta"
	"github.com/viant/forge/backend/types"
)

type resolutionHTTPSource struct {
	failure error
	late    bool
	reads   int
	raw     json.RawMessage
}

func (s *resolutionHTTPSource) Candidates(context.Context, identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	return []identity.ResourceCandidate{{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(s.raw)}}, nil
}
func (s *resolutionHTTPSource) ReadCandidate(context.Context, identity.ResourceURI, identity.ResourceCandidate) (json.RawMessage, error) {
	s.reads++
	if !s.late || s.reads >= 3 {
		return nil, s.failure
	}
	return s.raw, nil
}

type resolutionHTTPPolicy struct{}

func (resolutionHTTPPolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "actor", ValidUntil: time.Now().Add(time.Minute)}, nil
}
func TestCanonicalHTTPOpenPreservesTrustedResolutionFailures(t *testing.T) {
	for _, late := range []bool{false, true} {
		for _, test := range []struct {
			name    string
			failure error
			status  int
		}{
			{"timeout", context.DeadlineExceeded, 504}, {"unavailable", service.ErrWindowSelectionUnavailable, 503},
			{"public authz unavailable", authz.ErrUnavailable, 503},
			{"public gate unavailable", gating.ErrUnavailable, 503},
			{"native mapped unavailable", permittedview.ErrUnavailable, 503}, {"denied", identity.ErrResourceDenied, 404}, {"stale", identity.ErrResourceStale, 404}, {"unknown", errors.New("unavailable timeout 403 private text"), 404},
		} {
			t.Run(fmt.Sprintf("%s/late=%t", test.name, late), func(t *testing.T) {
				t.Setenv("AGENTLY_DEBUG", "off")
				var logs bytes.Buffer
				previousLog := log.Writer()
				log.SetOutput(&logs)
				t.Cleanup(func() { log.SetOutput(previousLog) })
				root := t.TempDir()
				source := &resolutionHTTPSource{failure: fmt.Errorf("private source detail: %w", test.failure), late: late, raw: json.RawMessage(`{"view":{"content":{"id":"safe"}}}`)}
				resolver := &identity.ResourceResolver{ProviderIdentity: "owner", Source: source, Policy: resolutionHTTPPolicy{}}
				catalog, err := service.NewMetadataWindowCatalog(meta.New(afs.New(), root), root, []service.SavedWindow{{WindowDefinitionSummary: service.WindowDefinitionSummary{WindowID: "view", ResourceURI: "window://team/view"}, Key: "view"}}, service.WithWindowResourceResolver(func(context.Context, string) (*identity.ResourceResolver, identity.ResourceRef, error) {
					return resolver, identity.ResourceRef{URI: "window://team/view"}, nil
				}))
				if err != nil {
					t.Fatal(err)
				}
				emissions := 0
				bridge := service.NewService(&service.Config{WindowDefinitions: catalog, WindowSelectionEmitter: func(_ context.Context, _ *types.Window, parameters map[string]any) (*identity.SelectionBinding, error) {
					emissions++
					digest, _ := identity.SelectionParametersDigest(parameters)
					return &identity.SelectionBinding{Actor: identity.CanonicalActor{Issuer: "fixture", Subject: "owner", TenantID: "team", AccountID: "account"}, InstanceID: "instance", ParametersDigest: digest, Token: "fixture-signed"}, nil
				}})
				handler := NewEmbeddedHandlerWithAuthorization(root, nil, nil, nil, WithWindowResourceProvider(bridge))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/view?applyPermission=true", nil))
				expected := test.status
				expectedEmissions := 0
				if late {
					if response.Code != http.StatusOK || emissions != 1 {
						t.Fatalf("first admitted request was reloaded: status=%d emissions=%d reads=%d", response.Code, emissions, source.reads)
					}
					var body struct {
						Data *types.Window `json:"data"`
					}
					if json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Data == nil || body.Data.View.Content.ID != "safe" {
						t.Fatal("original admitted frame lost")
					}
					response = httptest.NewRecorder()
					handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/view?applyPermission=true", nil))
					expectedEmissions = 1 // The next failed request must not emit another binding.
				}
				if response.Code != expected || emissions != expectedEmissions || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "selectionBinding") {
					t.Fatalf("canonical chain wrong status/release: %d %s emissions=%d reads=%d", response.Code, response.Body.String(), emissions, source.reads)
				}
				if !strings.Contains(logs.String(), `window.definition.failed stage="definition_read" window="view"`) || strings.Contains(logs.String(), "private source detail") {
					t.Fatalf("unsafe or missing operational diagnostic: %s", logs.String())
				}
			})
		}
	}
}
