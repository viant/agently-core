package sdk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/policy"
	"github.com/viant/authz"
)

type authorityErrorToolClient struct {
	Client
	failure error
}

func (c authorityErrorToolClient) ExecuteTool(context.Context, string, map[string]interface{}) (string, error) {
	return "restricted-definition", c.failure
}

func TestToolHTTPPreservesTypedAuthorityErrorsWithoutPartialDefinition(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		status  int
	}{
		{"policy", fmt.Errorf("tool failed: %w", policy.ErrDenied), 403},
		{"resource", fmt.Errorf("tool failed: %w", identity.ErrResourceDenied), 403},
		{"authz", fmt.Errorf("tool failed: %w", authz.ErrDenied), 403},
		{"identity", errors.Join(identity.ErrResourceDenied, authz.ErrIdentityDenied), 401},
		{"policy-identity", policy.ErrIdentityRejected, 401},
		{"unavailable", authz.ErrUnavailable, 503},
		{"untyped-policy-text", errors.New("policy evaluator failed unexpectedly"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, byName := range []bool{true, false} {
				request := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", strings.NewReader(`{"name":"ui/view:get","args":{"id":"restricted"}}`))
				response := httptest.NewRecorder()
				client := authorityErrorToolClient{failure: tc.failure}
				if byName {
					handleExecuteToolByName(client)(response, request)
				} else {
					request.SetPathValue("name", "ui/view:get")
					handleExecuteTool(client)(response, request)
				}
				if response.Code != tc.status {
					t.Fatalf("status=%d want=%d", response.Code, tc.status)
				}
				if (tc.status == 401 || tc.status == 403) && strings.Contains(response.Body.String(), "restricted-definition") {
					t.Fatal("authorization failure released partial definition")
				}
			}
		})
	}
}
