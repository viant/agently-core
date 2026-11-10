package skill

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	execconfig "github.com/viant/agently-core/app/executor/config"
)

func TestResolveRootsConfiguredListIsAuthoritative(t *testing.T) {
	root := t.TempDir()
	explicit := []string{filepath.Join(root, "managed"), filepath.Join(root, "shared")}
	got := ResolveRoots(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: append(append([]string(nil), explicit...), explicit[0])}})
	require.Equal(t, explicit, got, "configured roots retain precedence and never append ambient home directories")
}

func TestResolveRootsDefaultKeepsCompatibleHomeDirectories(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.NotEmpty(t, home)
	for _, defaults := range []*execconfig.Defaults{nil, {}} {
		roots := ResolveRoots(defaults)
		require.Contains(t, roots, filepath.Join(home, ".claude", "skills"))
		require.Contains(t, roots, filepath.Join(home, ".codex", "skills"))
	}
}
