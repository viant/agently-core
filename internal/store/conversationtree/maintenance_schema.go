package conversationtree

import (
	"context"
	"fmt"
	"strings"
)

type DriverInspector interface {
	ConfiguredDriver(context.Context, string) (string, error)
}

type maintenanceSchema struct{ sqlite bool }

// MaintenanceSchema retains the deployed-schema contract used by retention:
// SQLite excludes investigation and legacy schedule_run even if installed.
// This is configured-driver policy, not live table discovery.
func MaintenanceSchema(ctx context.Context, inspector DriverInspector) (TableInspector, error) {
	if inspector == nil {
		return nil, fmt.Errorf("maintenance configured driver is required")
	}
	driver, err := inspector.ConfiguredDriver(ctx, "agently")
	if err != nil {
		return nil, err
	}
	driver = strings.ToLower(strings.TrimSpace(driver))
	switch {
	case strings.Contains(driver, "sqlite"):
		return maintenanceSchema{sqlite: true}, nil
	case strings.Contains(driver, "mysql"):
		return maintenanceSchema{}, nil
	default:
		return nil, fmt.Errorf("unsupported deletion database driver %q", driver)
	}
}

func (s maintenanceSchema) HasTable(ctx context.Context, connector, table string) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("maintenance schema context is required")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if connector != "agently" || strings.TrimSpace(table) == "" {
		return false, fmt.Errorf("maintenance schema target is invalid")
	}
	table = strings.ToLower(strings.TrimSpace(table))
	return !(s.sqlite && (table == "investigation" || table == "schedule_run")), nil
}
