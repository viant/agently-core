package sdk

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	reporting "github.com/viant/forge/backend/reporting/engine"
)

type reportInputError struct{}

func (reportInputError) Error() string { return "private upstream detail" }
func (reportInputError) Unwrap() error { return reporting.ErrReportParameterBinding }
func (reportInputError) ReportParameterBindingDiagnostic() reporting.Diagnostic {
	return reporting.Diagnostic{Code: "undeclared_report_parameter", Severity: "error", Path: `parameters["extra"]`, Message: "Input is not declared.", SuggestedFix: "Remove the extra input."}
}

func TestToolReportInputFailureIsActionableWithoutInternalDetails(t *testing.T) {
	for _, byName := range []bool{true, false} {
		request := httptest.NewRequest(http.MethodPost, "/v1/tools/execute", strings.NewReader(`{"name":"reporting:run_report","args":{}}`))
		response := httptest.NewRecorder()
		client := authorityErrorToolClient{failure: fmt.Errorf("private dispatch: %w", reportInputError{})}
		if byName {
			handleExecuteToolByName(client)(response, request)
		} else {
			request.SetPathValue("name", "reporting:run_report")
			handleExecuteTool(client)(response, request)
		}
		require.Equal(t, http.StatusUnprocessableEntity, response.Code)
		require.Contains(t, response.Body.String(), "undeclared_report_parameter")
		require.NotContains(t, response.Body.String(), "private")
	}
	require.Equal(t, http.StatusForbidden, statusForToolExecuteError(errors.Join(identity.ErrResourceDenied, reportInputError{})))
}

type reportDateInputError struct{}

func (reportDateInputError) Error() string { return "private upstream detail" }

func (reportDateInputError) HTTPStatusCode() int { return 400 }
func (reportDateInputError) ReportParameterBindingDiagnostic() reporting.Diagnostic {
	return reporting.Diagnostic{Code: "report_input_validation", Severity: "error", Path: "to", Message: "End date cannot be in the future."}
}
func TestToolProducerInputDiagnosticPreservesMessageAndDenial(t *testing.T) {
	for _, denied := range []bool{false, true} {
		var err error = fmt.Errorf("internal dispatch: %w", reportDateInputError{})
		if denied {
			err = errors.Join(identity.ErrResourceDenied, err)
		}
		response := httptest.NewRecorder()
		writeToolExecuteError(response, err, "")
		if denied {
			require.Equal(t, 403, response.Code)
			require.NotContains(t, response.Body.String(), "End date")
		} else {
			require.Equal(t, 400, response.Code)
			require.Contains(t, response.Body.String(), "End date cannot be in the future.")
			require.Contains(t, response.Body.String(), `"path":"to"`)
		}
		require.NotContains(t, response.Body.String(), "internal dispatch")
	}
}
