package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	execconfig "github.com/viant/agently-core/app/executor/config"
)

func TestScreenshotRetentionDefaultsOverlay(t *testing.T) {
	maximum := 1
	base := &execconfig.Defaults{}
	override := &execconfig.Defaults{Projection: execconfig.Projection{ScreenshotRetention: &execconfig.ScreenshotRetention{MaxImages: &maximum}}}
	mergeDefaults(base, override)
	require.Equal(t, 1, base.Projection.ScreenshotImageLimit())
	disabled := false
	override.Projection.ScreenshotRetention.Enabled = &disabled
	mergeDefaults(base, override)
	require.Equal(t, -1, base.Projection.ScreenshotImageLimit())
}
