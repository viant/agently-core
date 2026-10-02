package delete

import (
	"context"
	"fmt"
	"strings"
)

// Init limits this private key-only contract to explicit deletion. Ordinary and
// conditional mutations use the full canonical run writer.
func (input *Input) Init(context.Context) error {
	for _, row := range input.Runs {
		if row == nil || row.Has == nil || !row.Has.Id || strings.TrimSpace(row.Id) == "" || !row.Has.ShouldDelete || !row.ShouldDelete {
			return fmt.Errorf("run delete requires marked identity and deletion")
		}
	}
	return nil
}
