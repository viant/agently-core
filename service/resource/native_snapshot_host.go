package resource

import (
	"context"
	"fmt"
	shared "github.com/viant/forge/backend/resourcehost"
)

// NewNativeAssetSnapshot installs Core's authored kinds into the shared snapshot compiler.
func NewNativeAssetSnapshot(ctx context.Context, root string, definitions []NativeSnapshotDefinition, options ...NativeSnapshotOptions) (*NativeAssetSnapshot, error) {
	if len(options) > 1 {
		return nil, fmt.Errorf("native snapshot requires at most one options value")
	}
	option := NativeSnapshotOptions{}
	if len(options) == 1 {
		option = options[0]
	}
	validators := map[string]LocalResourceValidator{"template": ValidateTemplateDefinition, "playbook": ValidateIntentDefinition, "intent": ValidateIntentDefinition, "skill": ValidateNativeSkillDefinition}
	for kind, validator := range option.Validators {
		validators[kind] = validator
	}
	option.Validators = validators
	return shared.NewNativeAssetSnapshot(ctx, root, definitions, option)
}
