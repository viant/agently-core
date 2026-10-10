package executor

import (
	"bytes"
	"context"
	"encoding/json"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	resourcesvc "github.com/viant/agently-core/service/resource"
	"github.com/viant/forge/backend/types"
)

func registerDatasourceReadAdmission(ctx context.Context, pin identity.ResolvedResource, target *types.WindowTarget, descriptor *dsproto.DataSource, inputs map[string]interface{}, check func(context.Context, identity.ResolvedResource, *dsproto.DataSource, map[string]interface{}) error) error {
	definition, err := json.Marshal(descriptor)
	if err != nil {
		return err
	}
	arguments, err := json.Marshal(inputs)
	if err != nil {
		return err
	}
	fingerprint, err := types.WindowDescriptorFingerprint(definition)
	if err != nil {
		return err
	}
	return resourcesvc.RegisterDatasourceSelectionAdmission(ctx, pin, target, descriptor.ID, fingerprint, func(checkCtx context.Context) error {
		currentDefinition, e := json.Marshal(descriptor)
		if e != nil || !bytes.Equal(definition, currentDefinition) {
			return identity.ErrResourceDenied
		}
		currentArguments, e := json.Marshal(inputs)
		if e != nil || !bytes.Equal(arguments, currentArguments) {
			return identity.ErrResourceDenied
		}
		var held dsproto.DataSource
		if json.Unmarshal(definition, &held) != nil {
			return identity.ErrResourceDenied
		}
		// Preserve the caller's Go scalar types; JSON guards make any mutation
		// fail closed before dispatch/release rather than changing argument types.
		if err := check(checkCtx, pin, &held, inputs); err != nil {
			return err
		}
		after, e := json.Marshal(inputs)
		if e != nil || !bytes.Equal(arguments, after) {
			return identity.ErrResourceDenied
		}
		return nil
	})
}
