package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
				bridge := service.NewService(&service.Config{WindowDefinitions: catalog, WindowSelectionEmitter: func(context.Context, *types.Window, map[string]any) (*identity.SelectionBinding, error) {
					emissions++
					return nil, nil
				}})
				handler := NewEmbeddedHandlerWithAuthorization(root, nil, nil, nil, WithWindowResourceProvider(bridge))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/view?applyPermission=true", nil))
				expected := test.status
				if late && (test.name == "unknown" || test.name == "denied" || test.name == "stale") {
					expected = 403
				}
				if response.Code != expected || emissions != 0 || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "selectionBinding") {
					t.Fatalf("canonical chain wrong status/release: %d %s emissions=%d reads=%d", response.Code, response.Body.String(), emissions, source.reads)
				}
			})
		}
	}
}
