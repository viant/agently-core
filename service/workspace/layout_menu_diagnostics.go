package workspace

import (
	"context"
	"errors"
	"net/http"

	"github.com/viant/agently-core/internal/logx"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

// layoutMenuFailure retains the failing step without changing the error seen
// by authorization and HTTP response code. Its identifiers are logged only
// after passing through SafeResourceIdentifier.
type layoutMenuFailure struct {
	stage, connection, resource string
	err                         error
}

func (e *layoutMenuFailure) Error() string { return e.err.Error() }
func (e *layoutMenuFailure) Unwrap() error { return e.err }

func markLayoutMenuFailure(stage, connection, resource string, err error) error {
	if err == nil {
		return nil
	}
	var existing *layoutMenuFailure
	if errors.As(err, &existing) {
		return err
	}
	return &layoutMenuFailure{stage: stage, connection: connection, resource: resource, err: err}
}

func layoutFailureCategory(err error) string {
	switch {
	case err == nil:
		return "invalid"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, identity.ErrResourceStale):
		return "stale"
	case errors.Is(err, identity.ErrResourceDenied):
		return "denied"
	}
	var unauthorized *jsonrpc.UnauthorizedError
	if errors.As(err, &unauthorized) && (unauthorized.StatusCode == http.StatusUnauthorized || unauthorized.StatusCode == http.StatusForbidden) {
		return "denied"
	}
	var rpc *jsonrpc.Error
	if errors.As(err, &rpc) && rpc.Code == schema.Unauthorized {
		return "denied"
	}
	var status interface{ HTTPStatusCode() int }
	if errors.As(err, &status) && (status.HTTPStatusCode() == http.StatusUnauthorized || status.HTTPStatusCode() == http.StatusForbidden) {
		return "denied"
	}
	return "other"
}

func logLayoutMenuFailure(stage string, err error) {
	connection, resource := "", ""
	var failure *layoutMenuFailure
	if errors.As(err, &failure) {
		stage, connection, resource = failure.stage, failure.connection, failure.resource
	}
	logx.OperationalWarnf("workspace-navigation", "layout.menu.failed stage=%q connection=%q resource=%q category=%q httpStatus=%d",
		stage, logx.SafeResourceIdentifier(connection), logx.SafeResourceIdentifier(resource), layoutFailureCategory(err), http.StatusServiceUnavailable)
}
