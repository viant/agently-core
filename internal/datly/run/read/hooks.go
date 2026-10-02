package read

import (
	"context"
	"fmt"
)

// Init validates the host-owned canonical reader mode.
func (input *RunRowsInput) Init(context.Context) error {
	switch input.ReadMode {
	case "rows":
		return nil
	case "active", "stale", "schedulerList", "schedulerRuns", "schedulerDue", "scheduledMaintenance":
		if input.InternalMode {
			return nil
		}
		return fmt.Errorf("run maintenance reads require trusted internal scope")
	default:
		return fmt.Errorf("unsupported run read mode")
	}
}
