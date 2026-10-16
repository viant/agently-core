package sdk

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/viant/jsonrpc"
)

func TestToolHTTPPreservesTypedMCPDenialWithoutProducerPayload(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		upstream := &jsonrpc.Error{Code: status, Message: "PRIVATE_PRODUCER_DETAILS", Data: []byte(`{"payload":"PRIVATE_BINARY_ECHO"}`)}
		for _, byName := range []bool{true, false} {
			request := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", strings.NewReader(`{"name":"reporting:run_report","args":{}}`))
			response := httptest.NewRecorder()
			client := authorityErrorToolClient{failure: fmt.Errorf("report dispatch: %w", upstream)}
			if byName {
				handleExecuteToolByName(client)(response, request)
			} else {
				request.SetPathValue("name", "reporting:run_report")
				handleExecuteTool(client)(response, request)
			}
			if response.Code != status {
				t.Errorf("status=%d want=%d", response.Code, status)
			}
			for _, private := range []string{"PRIVATE_PRODUCER_DETAILS", "PRIVATE_BINARY_ECHO", "restricted-definition"} {
				if strings.Contains(response.Body.String(), private) {
					t.Errorf("producer payload escaped: %s", private)
				}
			}
		}
	}
	for _, code := range []int{-32603, http.StatusConflict} {
		if got := statusForToolExecuteError(&jsonrpc.Error{Code: code, Message: "opaque managing-server decision"}); got != http.StatusInternalServerError {
			t.Errorf("unrelated code %d mapped to %d", code, got)
		}
	}
}
