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
