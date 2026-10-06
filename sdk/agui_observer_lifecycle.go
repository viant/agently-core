package sdk

import (
	"errors"
	"fmt"
	"strings"
)

// A transport observer loss is not a native execution outcome. The stopped
// worker leaves a running journal and lets its bounded lease expire; the next
// attachment can restore the graph and observe native persistence without Query.
var errAGUIObserverLost = errors.New("AG-UI observer detached")

func aguiObserverLost(reason string) error {
	return fmt.Errorf("%w: native subscription closed (%s)", errAGUIObserverLost, reason)
}
func aguiNativeObservationTerminal(native *aguiRecoveredNative) bool {
	if native == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(native.Status)) {
	case "completed", "finished", "success", "succeeded", "failed", "error", "canceled", "cancelled":
		return true
	case "waiting_for_user", "blocked":
		return len(native.Pending.Interrupts) > 0 || len(native.Pending.ClientTools) > 0
	}
	return false
}
