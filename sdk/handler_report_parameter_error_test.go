package sdk

import (
	"context"
	"errors"
	"fmt"
	authsvc "github.com/viant/agently-core/service/auth"
	primitiveprovider "github.com/viant/agently-core/service/primitiveprovider"
	personalreporting "github.com/viant/agently-core/service/reporting"
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

func TestUnavailableDefaultReturnsSafeRecoveryChoice(t *testing.T) {
	err := fmt.Errorf("%w: %w: private provider detail", personalreporting.ErrPersonalDefaultUnavailable, identity.ErrResourceDenied)
	response := httptest.NewRecorder()
	writeToolExecuteError(response, err, "private partial result")
	require.Equal(t, 403, response.Code)
	require.Contains(t, response.Body.String(), "open the original report")
	require.NotContains(t, response.Body.String(), "private")
}

func TestToolExecutePreservesExplicitDependencyOutage(t *testing.T) {
	response := httptest.NewRecorder()
	writeToolExecuteError(response, explicitInputStatusError{status: 503}, "")
	require.Equal(t, 503, response.Code)
}

func TestAcknowledgedReportOpenFailurePreservesPhaseAndStatus(t *testing.T) {
	for _, test := range []struct {
		cause  error
		status int
	}{{identity.ErrResourceDenied, 403}, {identity.ErrResourceStale, 409}, {context.DeadlineExceeded, 504}} {
		recorder := httptest.NewRecorder()
		writeToolExecuteError(recorder, errors.Join(primitiveprovider.ErrReportOpenUnconfirmed, fmt.Errorf("private detail: %w", test.cause)), "private output")
		require.Equal(t, test.status, recorder.Code)
		require.Contains(t, recorder.Body.String(), "browser acknowledged")
		require.NotContains(t, recorder.Body.String(), "private")
	}
}

func TestResetWithoutOriginalIsAnUnsupportedOperation(t *testing.T) {
	require.ErrorIs(t, personalreporting.ErrReportHasNoOriginal, errors.ErrUnsupported)
	recorder := httptest.NewRecorder()
	writeToolExecuteError(recorder, fmt.Errorf("private cause: %w", personalreporting.ErrReportHasNoOriginal), "private output")
	require.Equal(t, http.StatusNotImplemented, recorder.Code)
	require.Contains(t, recorder.Body.String(), "no verified original to restore")
	require.NotContains(t, recorder.Body.String(), "private")
}

func TestAmbiguousPersonalReportRequestsAChoiceThroughHTTP(t *testing.T) {
	const uri = "report://example/summary"
	service := &personalreporting.Service{}
	service.SetPersonalWorkspacePreferences(func(context.Context) (*authsvc.WorkspacePreferences, error) {
		return &authsvc.WorkspacePreferences{Defaults: []authsvc.WorkspaceReportDefault{
			{SourceProvider: "first", SourceURI: uri, TargetURI: "report://users/owner/first"},
			{SourceProvider: "second", SourceURI: uri, TargetURI: "report://users/owner/second"},
		}}, nil
	})
	_, err := service.GetReport(t.Context(), &personalreporting.GetReportInput{ArtifactRef: uri, PreferPersonal: true})
	require.Error(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/tools/reporting:get_report/execute", strings.NewReader("{}"))
	req.SetPathValue("name", "reporting:get_report")
	response := httptest.NewRecorder()
	handleExecuteTool(authorityErrorToolClient{failure: fmt.Errorf("private dispatch: %w", err)})(response, req)
	require.Equal(t, http.StatusConflict, response.Code)
	require.Contains(t, response.Body.String(), "choose a personal report or open the original explicitly")
	require.NotContains(t, response.Body.String(), "private")
}
