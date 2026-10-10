package elicitation

import "errors"

// ErrNotFound identifies a missing elicitation after conversation ownership has
// been checked. Callers must keep ownership failures opaque.
var ErrNotFound = errors.New("elicitation message not found")

// ErrConflict identifies an answer that cannot replace the committed decision
// or resolve a terminal request without an exact original answer receipt.
var ErrConflict = errors.New("elicitation resolution conflict")
