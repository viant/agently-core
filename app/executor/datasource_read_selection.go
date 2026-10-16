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

// Keep the selected descriptor and original argument values stable through
// dispatch; resource authorization belongs to the managing MCP.
func registerDatasourceReadIntegrity(ctx context.Context, pin identity.ResolvedResource, target *types.WindowTarget, descriptor *dsproto.DataSource, inputs map[string]interface{}) error {
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
	return resourcesvc.RegisterDatasourceSelectionAdmission(ctx, pin, target, descriptor.ID, fingerprint, func(context.Context) error {
		currentDefinition, e := json.Marshal(descriptor)
		if e != nil || !bytes.Equal(definition, currentDefinition) {
			return identity.ErrResourceDenied
		}
		currentArguments, e := json.Marshal(inputs)
		if e != nil || !bytes.Equal(arguments, currentArguments) {
			return identity.ErrResourceDenied
		}
		return nil
	})
}
