package read

import (
	"context"

	turn "github.com/viant/agently-core/pkg/agently/turn/queuedList"
	"github.com/viant/datly"
)

type QueuedListInput = turn.QueuedTurnsInput
type QueuedListInputHas = turn.QueuedTurnsInputHas
type QueuedListOutput = turn.QueuedTurnsOutput

type QueuedTurnView = turn.QueuedTurnsView

var QueuedListPathURI = turn.QueuedTurnsPathURI

func DefineQueuedListComponent(ctx context.Context, srv *datly.Service) error {
	return turn.DefineQueuedTurnsComponent(ctx, srv)
}
