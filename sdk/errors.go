package sdk

import "errors"

type conflictError struct {
	msg string
}

func (e *conflictError) Error() string {
	if e == nil {
		return "conflict"
	}
	return e.msg
}

func newConflictError(msg string) error {
	return &conflictError{msg: msg}
}

func isConflictError(err error) bool {
	var target *conflictError
	return errors.As(err, &target)
}
