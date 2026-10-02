package cube

import (
	"context"
	"fmt"
)

func (input *RunReportInput) Init(context.Context) error {
	if input.ReportMode != "runs" && input.ReportMode != "scheduler" {
		return fmt.Errorf("unsupported run report mode")
	}
	if input.ReportMode == "scheduler" && !input.InternalMode {
		return fmt.Errorf("scheduler reports require trusted host scope")
	}
	return nil
}
