package sdk

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	toolregistry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"github.com/viant/authz"
)

type localAuthorityInput struct {
	ID string `json:"id"`
}
type localAuthorityOutput struct {
	Definition string `json:"definition"`
}
type localAuthorityService struct{}

func (*localAuthorityService) Name() string { return "authority-fixture" }
func (*localAuthorityService) Methods() svc.Signatures {
	return svc.Signatures{{Name: "get", Input: reflect.TypeOf(localAuthorityInput{}), Output: reflect.TypeOf(localAuthorityOutput{})}}
}
func (*localAuthorityService) Method(string) (svc.Executable, error) {
	return func(ctx context.Context, in, out interface{}) error {
		time.Sleep(time.Millisecond)
		out.(*localAuthorityOutput).Definition = "restricted-definition"
		switch in.(*localAuthorityInput).ID {
		case "resource":
			return identity.ErrResourceDenied
		case "identity":
			return authz.ErrIdentityDenied
		case "unavailable":
			return authz.ErrUnavailable
		case "untyped":
			return errors.New("resource revision denied")
		default:
			out.(*localAuthorityOutput).Definition = "allowed-definition"
			return nil
		}
	}, nil
}

func TestRealLocalServiceRegistrySDKPreservesAuthorityErrors(t *testing.T) {
	mgr, err := manager.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := toolregistry.NewWithManager(mgr)
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.AddInternalService(&localAuthorityService{}); err != nil {
		t.Fatal(err)
	}
	client := &backendClient{registry: registry}
	for _, tc := range []struct {
		id      string
		status  int
		failure error
	}{{"resource", 403, identity.ErrResourceDenied}, {"identity", 401, authz.ErrIdentityDenied}, {"unavailable", 503, authz.ErrUnavailable}, {"untyped", 500, nil}, {"allowed", 200, nil}} {
		t.Run(tc.id, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", strings.NewReader(`{"name":"authority-fixture:get","args":{"id":"`+tc.id+`"}}`))
			response := httptest.NewRecorder()
			handleExecuteToolByName(client)(response, request)
			if response.Code != tc.status {
				t.Fatalf("real local registry SDK status=%d want=%d", response.Code, tc.status)
			}
			if tc.status != 200 && strings.Contains(response.Body.String(), "restricted-definition") {
				t.Fatal("failure released partial service output")
			}
			if tc.failure != nil {
				_, err := client.ExecuteTool(context.Background(), "authority-fixture:get", map[string]interface{}{"id": tc.id})
				if !errors.Is(err, tc.failure) {
					t.Fatalf("internal service error lost identity: %T", err)
				}
			}

		})
	}
	// One shared registered service handles two actors concurrently. Error
	// carriers must remain invocation-local and never poison the successful call.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for run := 0; run < 20; run++ {
				id, want := "resource", 403
				if i == 1 {
					id, want = "allowed", 200
				}
				req := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", strings.NewReader(`{"name":"authority-fixture:get","args":{"id":"`+id+`"}}`))
				req = req.WithContext(iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: []string{"actor-denied", "actor-allowed"}[i]}))
				response := httptest.NewRecorder()
				handleExecuteToolByName(client)(response, req)
				if response.Code != want {
					t.Errorf("concurrent actor %d status=%d want=%d", i, response.Code, want)
				}
			}
		}(i)
	}
	wg.Wait()
}
