
package elicitation

import (
	"context"
	plan "github.com/viant/agently-core/protocol/agent/plan"
	stdioprompt "github.com/viant/agently-core/service/elicitation/stdio"
	"io"
)

func Prompt(ctx context.Context, w io.Writer, r io.Reader, p *plan.Elicitation) (*plan.ElicitResult, error) {
	return stdioprompt.Prompt(ctx, w, r, p)
}
