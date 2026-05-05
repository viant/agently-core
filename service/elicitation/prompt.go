package elicitation

import (
	"context"
	"io"

	plan "github.com/viant/agently-core/protocol/agent/execution"
	stdioprompt "github.com/viant/agently-core/service/elicitation/stdio"
)

func Prompt(ctx context.Context, w io.Writer, r io.Reader, p *plan.Elicitation) (*plan.ElicitResult, error) {
	return stdioprompt.Prompt(ctx, w, r, p)
}
