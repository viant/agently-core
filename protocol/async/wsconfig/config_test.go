package wsconfig

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	asynccfg "github.com/viant/agently-core/protocol/async"
	asyncnarrator "github.com/viant/agently-core/protocol/async/narrator"
)

func TestParseWorkspaceConfig_EmptyIsNoop(t *testing.T) {
	cfg, err := ParseWorkspaceConfig(nil)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Nil(t, cfg.GC)
	require.Nil(t, cfg.Narrator)
}

func TestParseWorkspaceConfig_Full(t *testing.T) {
	data := []byte(`
gc:
  interval: 5m
  maxAge: 1h
narrator:
  llmTimeout: 2500ms
`)
	cfg, err := ParseWorkspaceConfig(data)
	require.NoError(t, err)
	require.Equal(t, "5m", cfg.GC.Interval)
	require.Equal(t, "1h", cfg.GC.MaxAge)
	require.Equal(t, "2500ms", cfg.Narrator.LLMTimeout)
}

func TestParseWorkspaceConfig_MalformedYamlFailsLoudly(t *testing.T) {
	_, err := ParseWorkspaceConfig([]byte("gc: [not-an-object"))
	require.Error(t, err)
}

func TestApply_AppliesNarratorTimeoutAndStartsGC(t *testing.T) {
	// Save & restore the package-level narrator timeout so other tests
	// are unaffected. Zero = "no timeout" (package-level default after
	// defaults migrated to the workspace baseline).
	defer asyncnarrator.SetLLMTimeout(0)

	cfg := &WorkspaceConfig{
		GC:       &GCConfig{Interval: "1h", MaxAge: "24h"},
		Narrator: &NarratorConfig{LLMTimeout: "500ms"},
	}

	manager := asynccfg.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gcInterval, gcMaxAge, narratorTimeout, err := cfg.Apply(ctx, manager)
	require.NoError(t, err)
	require.Equal(t, time.Hour, gcInterval)
	require.Equal(t, 24*time.Hour, gcMaxAge)
	require.Equal(t, 500*time.Millisecond, narratorTimeout)
}

func TestApply_InvalidDurationFailsLoudly(t *testing.T) {
	cfg := &WorkspaceConfig{
		GC: &GCConfig{Interval: "nope"},
	}
	_, _, _, err := cfg.Apply(context.Background(), asynccfg.NewManager())
	require.Error(t, err)
	require.Contains(t, err.Error(), "gc.interval")
}

func TestApply_NilManagerDoesNotStartGC(t *testing.T) {
	cfg := &WorkspaceConfig{
		GC: &GCConfig{Interval: "1h", MaxAge: "24h"},
	}
	// Should not panic on nil manager.
	gcInterval, gcMaxAge, _, err := cfg.Apply(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, time.Hour, gcInterval)
	require.Equal(t, 24*time.Hour, gcMaxAge)
}

func TestApply_NilConfigIsNoop(t *testing.T) {
	var cfg *WorkspaceConfig
	gcInterval, gcMaxAge, narratorTimeout, err := cfg.Apply(context.Background(), asynccfg.NewManager())
	require.NoError(t, err)
	require.Zero(t, gcInterval)
	require.Zero(t, gcMaxAge)
	require.Zero(t, narratorTimeout)
}
