package read

import (
	turn "github.com/viant/agently-core/pkg/agently/turn/byId"
)

type TurnByIDInput = turn.TurnLookupInput
type TurnByIDInputHas = turn.TurnLookupInputHas
type TurnByIDOutput = turn.TurnLookupOutput

type TurnByIDView = turn.TurnLookupView

var TurnByIDPathURI = turn.TurnLookupPathURI
