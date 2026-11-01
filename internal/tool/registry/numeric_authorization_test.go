package tool

import (
	"context"
	"encoding/json"
	"errors"
	ds "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	"testing"
)

func TestNumericMCPProvenanceSurvivesActualRegistryDispatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload any
		want    int
	}{{"numeric403", map[string]any{"code": 403}, 403}, {"numeric401", map[string]any{"statusCode": 401}, 401}, {"text only", map[string]any{"message": "403 forbidden"}, 0}, {"forbidden string", map[string]any{"status": "forbidden"}, 0}, {"mixed", map[string]any{"status": 401, "code": 403}, 401}, {"exact JSON number", map[string]any{"statusCode": json.Number("403")}, 403}, {"large numeric code", map[string]any{"code": json.Number("4294967699")}, 0}, {"fractional numeric code", map[string]any{"code": json.Number("403.5")}, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			failed := true
			cli := &scriptedCallClient{result: &schema.CallToolResult{IsError: &failed, StructuredContent: tc.payload}}
			mgr := &scriptedReconnectManager{clients: []mcpclient.Interface{cli}}
			registry := newReconnectTestRegistry(mgr)
			_, err := registry.Execute(context.Background(), "fixture:read", nil)
			var numeric *ds.UpstreamAuthorizationError
			if tc.want == 0 {
				if errors.As(err, &numeric) {
					t.Fatal("text created numeric provenance", err)
				}
			} else if !errors.As(err, &numeric) || numeric.Status != tc.want {
				t.Fatal("numeric status lost", err)
			}
		})
	}
}

func TestNumericMCPValidationStatusRequiresExplicitErrorStatus(t *testing.T) {
	failed := true
	for _, tc := range []struct {
		payload any
		want    int
	}{
		{map[string]any{"statusCode": 400}, 400},
		{map[string]any{"statusCode": json.Number("422")}, 422},
		{map[string]any{"error": map[string]any{"statusCode": 400}}, 400},
		{map[string]any{"code": 400}, 0},
		{map[string]any{"statusCode": "400"}, 0},
		{map[string]any{"statusCode": 400.5}, 0},
		{map[string]any{"message": "400"}, 0},
		{map[string]any{"statusCode": 400, "error": map[string]any{"code": 403}}, 403},
	} {
		got := numericMCPErrorStatus(&schema.CallToolResult{IsError: &failed, StructuredContent: tc.payload})
		if got != tc.want {
			t.Fatalf("numeric status got %d want %d", got, tc.want)
		}
	}
	result := &schema.CallToolResult{StructuredContent: map[string]any{"statusCode": 400}}
	if got := numericMCPErrorStatus(result); got != 0 {
		t.Fatalf("successful payload classified as error: %d", got)
	}
	result.IsError = &failed
	result.Content = []schema.CallToolResultContentElem{schema.TextContent{Type: "text", Text: `{"statusCode":401}`}}
	if got := numericMCPErrorStatus(result); got != 401 {
		t.Fatalf("authorization status did not win: %d", got)
	}
}

func TestProducerInputDiagnosticSurvivesRegistryDispatch(t *testing.T) {
	failed := true
	for _, denied := range []bool{false, true} {
		payload := map[string]any{"statusCode": 400, "code": "report_input_validation", "fieldPath": "to", "message": "End date cannot be in the future."}
		if denied {
			payload["error"] = map[string]any{"statusCode": 403}
		}
		cli := &scriptedCallClient{result: &schema.CallToolResult{IsError: &failed, StructuredContent: payload}}
		registry := newReconnectTestRegistry(&scriptedReconnectManager{clients: []mcpclient.Interface{cli}})
		_, err := registry.Execute(context.Background(), "fixture:read", nil)
		var input *upstreamMCPInputError
		if denied {
			var authError *ds.UpstreamAuthorizationError
			if !errors.As(err, &authError) || authError.Status != 403 || errors.As(err, &input) {
				t.Fatalf("denial must take precedence: %v", err)
			}
			continue
		}
		if !errors.As(err, &input) {
			t.Fatalf("lost typed input error: %v", err)
		}
		diagnostic := input.ReportParameterBindingDiagnostic()
		if diagnostic.Code != "report_input_validation" || diagnostic.Path != "to" || diagnostic.Message != payload["message"] {
			t.Fatalf("lost input diagnostic: %+v", diagnostic)
		}
	}
}
