package read

import (
	"context"

	turn "github.com/viant/agently-core/pkg/agently/turn/byId"
	"github.com/viant/datly"
)

type TurnByIDInput = turn.TurnLookupInput
type TurnByIDInputHas = turn.TurnLookupInputHas
type TurnByIDOutput = turn.TurnLookupOutput

type TurnByIDView = turn.TurnLookupView

var TurnByIDPathURI = turn.TurnLookupPathURI

func DefineTurnByIDComponent(ctx context.Context, srv *datly.Service) error {
	return turn.DefineTurnLookupComponent(ctx, srv)
}
