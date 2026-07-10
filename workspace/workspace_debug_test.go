package workspace

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceDebugLogging_DisabledByDefault(t *testing.T) {
	resetWorkspaceStateForTest(t)
	root := filepath.Join(t.TempDir(), "workspace")
	t.Setenv(envKey, root)
	t.Setenv(debugEnvKey, "")
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")

	output := captureLog(t, func() {
		require.Equal(t, root, Root())
	})

	require.Empty(t, output)
	info, err := os.Stat(root)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestWorkspaceDebugLogging_LogsCreationAndBootstrap(t *testing.T) {
	resetWorkspaceStateForTest(t)
	root := filepath.Join(t.TempDir(), "workspace")
	t.Setenv(envKey, root)
	t.Setenv(debugEnvKey, "1")

	output := captureLog(t, func() {
		require.Equal(t, root, Root())
	})

	require.Contains(t, output, "[debug][workspace] created directory at "+root)
	require.Contains(t, output, "[debug][workspace] bootstrapping defaults at "+root)
	require.FileExists(t, filepath.Join(root, "config.yaml"))
}

func TestWorkspaceDebugLogging_DoesNotLogExistingDirectoryCreation(t *testing.T) {
	resetWorkspaceStateForTest(t)
	root := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.MkdirAll(root, 0o755))
	t.Setenv(envKey, root)
	t.Setenv(debugEnvKey, "1")
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")

	output := captureLog(t, func() {
		require.Equal(t, root, Root())
	})

	require.NotContains(t, output, "created directory")
	require.Empty(t, strings.TrimSpace(output))
}

func resetWorkspaceStateForTest(t *testing.T) {
	t.Helper()
	defaultsMu.Lock()
	oldExplicitRoot := explicitRoot
	oldCachedRoot := cachedRoot
	oldCachedRuntime := cachedRuntime
	oldCachedState := cachedState
	oldDefaultsByRoot := defaultsByRoot
	oldBootstrapHook := bootstrapHook
	oldBootstrapAssetsFS := bootstrapAssetsFS
	oldBootstrapAssetsPrefix := bootstrapAssetsPrefix
	oldBootstrapTemplateVars := bootstrapTemplateVars
	explicitRoot = ""
	cachedRoot = ""
	cachedRuntime = ""
	cachedState = ""
	defaultsByRoot = map[string]bool{}
	bootstrapHook = nil
	bootstrapAssetsFS = defaultWorkspaceFS
	bootstrapAssetsPrefix = "defaults"
	bootstrapTemplateVars = nil
	defaultsMu.Unlock()

	t.Cleanup(func() {
		defaultsMu.Lock()
		explicitRoot = oldExplicitRoot
		cachedRoot = oldCachedRoot
		cachedRuntime = oldCachedRuntime
		cachedState = oldCachedState
		defaultsByRoot = oldDefaultsByRoot
		bootstrapHook = oldBootstrapHook
		bootstrapAssetsFS = oldBootstrapAssetsFS
		bootstrapAssetsPrefix = oldBootstrapAssetsPrefix
		bootstrapTemplateVars = oldBootstrapTemplateVars
		defaultsMu.Unlock()
	})
}

func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	originalPrefix := log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	defer func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
		log.SetPrefix(originalPrefix)
	}()

	fn()
	return buf.String()
}
