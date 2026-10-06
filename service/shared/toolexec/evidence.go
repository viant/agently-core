package toolexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/viant/agently-core/runtime/evidence"
)

// prepareEvidenceRequest runs before request payload capture, coalescing and
// dispatch. The server's effective arguments are the arguments all three see.
func prepareEvidenceRequest(ctx context.Context, step StepInfo) (StepInfo, error) {
	hook := evidence.ToolsFromContext(ctx)
	if hook == nil {
		return step, nil
	}
	raw, err := json.Marshal(step.Args)
	if err != nil {
		return step, err
	}
	effective, handled, err := hook.Prepare(ctx, step.Name, step.ID, raw)
	if err != nil {
		return step, err
	}
	if !handled {
		return step, nil
	}
	var args map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(effective))
	decoder.UseNumber()
	if err = decoder.Decode(&args); err != nil || args == nil || !json.Valid(effective) {
		return step, fmt.Errorf("evidence preparation returned invalid arguments")
	}
	step.Args = args
	return step, nil
}
