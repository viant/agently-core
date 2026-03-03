package read

import (
	"context"

	turn "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
	"github.com/viant/datly"
)

type QueuedCountInput = turn.QueuedTotalInput
type QueuedCountInputHas = turn.QueuedTotalInputHas
type QueuedCountOutput = turn.QueuedTotalOutput

type QueuedCountView = turn.QueuedTotalView

var QueuedCountPathURI = turn.QueuedTotalPathURI

func DefineQueuedCountComponent(ctx context.Context, srv *datly.Service) error {
	return turn.DefineQueuedTotalComponent(ctx, srv)
}
