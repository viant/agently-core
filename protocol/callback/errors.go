package callback

import "errors"

var (
	errNilCallback  = errors.New("callback: nil config")
	errEmptyID      = errors.New("callback.id must not be empty")
	errEmptyTool    = errors.New("callback.tool must not be empty")
	errEmptyPayload = errors.New("callback.payload.body must not be empty")
)
