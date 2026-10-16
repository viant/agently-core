package sdk

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/viant/jsonrpc"
)

func TestDatasourcePreservesTypedMCPAuthorizationStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		upstream := &jsonrpc.Error{Code: status, Message: "opaque managing-server decision"}
		for _, err := range []error{upstream, fmt.Errorf("datasource dispatch: %w", upstream)} {
			if got := statusForDatasourceErr(err); got != status {
				t.Errorf("typed upstream code %d mapped to %d", status, got)
			}
		}
	}
	for _, code := range []int{-32603, http.StatusConflict} {
		if got := statusForDatasourceErr(&jsonrpc.Error{Code: code, Message: "opaque managing-server decision"}); got != http.StatusInternalServerError {
			t.Errorf("unrelated upstream code %d mapped to %d", code, got)
		}
	}
}

func TestDatasourceTypedMCPDenialOmitsProducerErrorPayload(t *testing.T) {
	upstream := &jsonrpc.Error{Code: http.StatusForbidden, Message: "private producer details", Data: []byte(`{"payload":"PRIVATE_BINARY_ECHO"}`)}
	response := httptest.NewRecorder()
	writeDatasourceError(response, fmt.Errorf("datasource dispatch: %w", upstream))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
	if body := response.Body.String(); strings.Contains(body, "private producer details") || strings.Contains(body, "PRIVATE_BINARY_ECHO") {
		t.Fatal("producer error payload escaped")
	}
}
