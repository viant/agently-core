package layoutgate

import (
	"context"
	"errors"
	"fmt"
	"github.com/viant/agently-core/service/ui/permittedview"
	forgetypes "github.com/viant/forge/backend/types"
	"time"
)

var ErrFactsUnavailable = errors.New("authorization facts unavailable")

func ResolveNode(ctx context.Context, spec *forgetypes.AuthorizationSpec, condition any, inherited *permittedview.Snapshot, runtime *permittedview.Runtime) (bool, *permittedview.Snapshot, error) {
	snapshot := inherited
	if spec != nil {
		if runtime == nil || runtime.Resolver == nil {
			return false, nil, ErrFactsUnavailable
		}
		if spec.Resource != nil {
			return false, nil, fmt.Errorf("resource-bound shell authorization requires parameter binding")
		}
		if spec.ResourceType == "" {
			return false, nil, fmt.Errorf("authorization resource type required")
		}
		schemaVersion := permittedview.SchemaVersion(spec)
		if schemaVersion != 1 && schemaVersion != 2 {
			return false, nil, fmt.Errorf("unsupported authorization schema version")
		}
		var err error
		snapshot, err = runtime.Resolver.Resolve(ctx, &permittedview.Request{SchemaVersion: schemaVersion, ResourceType: spec.ResourceType, RequestedGlobalCapabilities: spec.RequestedGlobalCapabilities, IncludePrincipal: true})
		if err != nil {
			return false, nil, err
		}
		if snapshot == nil || snapshot.AuthorizationVersion == "" || !time.Now().Before(snapshot.ExpiresAt) || (schemaVersion == 2 && snapshot.SchemaVersion != 2) {
			return false, nil, fmt.Errorf("invalid authorization snapshot")
		}
	}
	if condition == nil {
		return true, snapshot, nil
	}
	if snapshot == nil {
		return false, nil, ErrFactsUnavailable
	}
	allowed, err := permittedview.EvaluateAuthorizationCondition(condition, snapshot, nil)
	return allowed, snapshot, err
}
