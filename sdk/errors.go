package sdk

import (
	"errors"
	"strings"
)

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

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "not found") || strings.Contains(msg, "no rows")
}
