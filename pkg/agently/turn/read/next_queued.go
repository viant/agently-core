package read

import (
	"context"

	turn "github.com/viant/agently-core/pkg/agently/turn/nextQueued"
	"github.com/viant/datly"
)

type NextQueuedInput = turn.QueuedTurnInput
type NextQueuedInputHas = turn.QueuedTurnInputHas
type NextQueuedOutput = turn.QueuedTurnOutput

type NextQueuedView = turn.QueuedTurnView

var NextQueuedPathURI = turn.QueuedTurnPathURI

func DefineNextQueuedComponent(ctx context.Context, srv *datly.Service) error {
	return turn.DefineQueuedTurnComponent(ctx, srv)
}
