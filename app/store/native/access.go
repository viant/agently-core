package native

import (
	"context"
	"reflect"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/bindly/locator"
	"github.com/viant/datly/runtime/handler/provider"
)

// Access describes service-owned row scope. Internal permits trusted worker
// reads through declared predicates; it does not change Datly's global
// internal(true) route visibility or internal(view.column) field visibility.
// Transport query or body values never produce this context value.
type Access struct {
	Internal   bool
	Mode       string
	ReportMode bool
	ListMode   bool
}

type accessKey struct{}

// WithAccess attaches scope chosen by the caller after authentication and
// route policy have been checked. Public requests should leave Internal false.
func WithAccess(ctx context.Context, access Access) context.Context {
	return context.WithValue(ctx, accessKey{}, access)
}

func accessFromContext(ctx context.Context) (Access, bool) {
	value, ok := ctx.Value(accessKey{}).(Access)
	return value, ok
}

// AccessProviders supplies the private Datly inputs from trusted context. A
// missing access scope leaves required component access bindings unresolved.
func AccessProviders() []locator.Provider {
	kinds := []string{
		"approvalaccess", "artifactaccess", "claimaccess", "conversationaccess",
		"investigationaccess", "linkstateaccess", "messageaccess", "modelcallaccess",
		"maintenanceaccess",
		"reportaccess", "reportauditaccess", "runaccess", "scheduleaccess",
		"schedulerunaccess", "toolcallaccess", "turnaccess",
	}
	result := make([]locator.Provider, 0, len(kinds)+1)
	for _, kind := range kinds {
		result = append(result, provider.Named(kind, func(ctx context.Context, _ reflect.Type, name string) (any, bool, error) {
			access, ok := accessFromContext(ctx)
			if !ok {
				return nil, false, nil
			}
			switch name {
			case "internal":
				return access.Internal, true, nil
			case "mode":
				return strings.TrimSpace(access.Mode), true, nil
			case "reportMode":
				return access.ReportMode, true, nil
			case "list":
				return access.ListMode, true, nil
			case "enforceVisibility":
				return access.ListMode, true, nil
			case "ascending":
				return false, true, nil
			default:
				return nil, false, nil
			}
		}))
	}
	result = append(result, provider.Named("visibility", func(ctx context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name != "subject" {
			return nil, false, nil
		}
		subject := strings.TrimSpace(authctx.EffectiveUserID(ctx))
		return &subject, true, nil
	}))
	return result
}
