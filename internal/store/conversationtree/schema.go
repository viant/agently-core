package conversationtree

import (
	"context"
	"fmt"
)

// TableInspector is the stock linked host's read-only SQLX metadata surface.
type TableInspector interface {
	HasTable(context.Context, string, string) (bool, error)
}

func (d *Discoverer) hasTable(ctx context.Context, table string) (bool, error) {
	inspector := d.Schema
	if inspector == nil {
		if available, ok := d.Invoker.(TableInspector); ok {
			inspector = available
		}
	}
	if inspector == nil {
		return false, fmt.Errorf("conversation graph requires linked schema metadata")
	}
	present, err := inspector.HasTable(ctx, "agently", table)
	if err != nil {
		return false, fmt.Errorf("inspect %s table: %w", table, err)
	}
	return present, nil
}
