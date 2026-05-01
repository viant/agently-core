package toolvalidate

import (
	"context"

	"github.com/viant/agently-core/protocol/tool"
	skillsvc "github.com/viant/agently-core/service/skill"
)

// ValidateExecution applies both global approval policy and active-skill
// constraints. Approval policy remains the outer gate; active-skill
// constraints then narrow what an already-approved tool may do.
func ValidateExecution(ctx context.Context, policy *tool.Policy, name string, args map[string]interface{}) error {
	if err := tool.ValidateExecution(ctx, policy, name, args); err != nil {
		return err
	}
	return skillsvc.ValidateExecution(ctx, name, args)
}
