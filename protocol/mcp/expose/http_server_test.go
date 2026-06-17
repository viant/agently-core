package expose

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerConfig_ListenAddr_DefaultsToLoopback5000(t *testing.T) {
	cfg := &ServerConfig{
		ToolItems: []string{"system/*"},
	}

	assert.True(t, cfg.Enabled())
	assert.Equal(t, DefaultPort, cfg.EffectivePort())
	assert.Equal(t, "127.0.0.1:5000", cfg.ListenAddr())
}

func TestServerConfig_ListenAddr_PrefersExplicitAddr(t *testing.T) {
	cfg := &ServerConfig{
		Addr:      "0.0.0.0:9192",
		ToolItems: []string{"system/*"},
	}

	assert.True(t, cfg.Enabled())
	assert.Equal(t, 0, cfg.EffectivePort())
	assert.Equal(t, "0.0.0.0:9192", cfg.ListenAddr())
}

func TestNewHTTPServer_UsesConfiguredAddr(t *testing.T) {
	srv, err := NewHTTPServer(context.Background(), &stubExec{}, &ServerConfig{
		Addr:      "127.0.0.1:5999",
		ToolItems: []string{"system/*"},
	})
	require.NoError(t, err)
	require.NotNil(t, srv)
	assert.Equal(t, "127.0.0.1:5999", srv.Addr)
}
