package read

import (
	"context"

	turn "github.com/viant/agently-core/pkg/agently/turn/active"
	"github.com/viant/datly"
)

type ActiveTurnInput = turn.ActiveTurnsInput
type ActiveTurnInputHas = turn.ActiveTurnsInputHas
type ActiveTurnOutput = turn.ActiveTurnsOutput

type ActiveTurnView = turn.ActiveTurnsView

var ActiveTurnPathURI = turn.ActiveTurnsPathURI

func DefineActiveTurnComponent(ctx context.Context, srv *datly.Service) error {
	return turn.DefineActiveTurnsComponent(ctx, srv)
}
