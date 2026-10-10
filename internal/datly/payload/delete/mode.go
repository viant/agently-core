package delete

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const ModeEnvironment = "AGENTLY_DELETE_PAYLOAD_MODE"

type Mode string

const (
	RowMode  Mode = "row"
	BulkMode Mode = "bulk"
)

type modeKey struct{}

// PinMode selects once before the enclosing deletion transaction begins.
// Nested graph/chunk invocations inherit it; invalid settings never fall back.
func PinMode(ctx context.Context) (context.Context, error) {
	if _, ok := ctx.Value(modeKey{}).(Mode); ok {
		return ctx, nil
	}
	mode := Mode(strings.TrimSpace(os.Getenv(ModeEnvironment)))
	if mode == "" {
		mode = BulkMode
	}
	if mode != RowMode && mode != BulkMode {
		return ctx, fmt.Errorf("%s must be row or bulk, got %q", ModeEnvironment, mode)
	}
	return context.WithValue(ctx, modeKey{}, mode), nil
}

// PinnedMode is only consumed after PinMode at a deletion entry point.
func PinnedMode(ctx context.Context) Mode {
	if mode, ok := ctx.Value(modeKey{}).(Mode); ok {
		return mode
	}
	return BulkMode
}
