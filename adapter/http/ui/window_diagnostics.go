package ui

import (
	"context"
	"errors"
	"net/http"

	"github.com/viant/agently-core/internal/logx"
	identity "github.com/viant/agently-core/protocol/resource"
	forgeservice "github.com/viant/agently-core/service/primitiveprovider"
)

func logWindowDefinitionFailure(key string, err error) {
	stage := forgeservice.WindowFailureStage(err)
	if stage == "" {
		stage = "request_or_catalog"
	}
	category, status := "other", http.StatusNotFound
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		category, status = "timeout", http.StatusGatewayTimeout
	case errors.Is(err, forgeservice.ErrWindowSelectionUnavailable):
		category, status = "unavailable", http.StatusServiceUnavailable
	case errors.Is(err, context.Canceled):
		category = "canceled"
	case errors.Is(err, identity.ErrResourceStale):
		category = "stale"
	case errors.Is(err, identity.ErrResourceDenied):
		category = "denied"
	}
	logx.OperationalWarnf("workspace-navigation", "window.definition.failed stage=%q window=%q category=%q httpStatus=%d",
		stage, logx.SafeResourceIdentifier(key), category, status)
}
