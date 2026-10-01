package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var legacyProbeBuild struct {
	once                    sync.Once
	directory, root, binary string
	err                     error
}

// Build the real SDK0 probe once per Go test invocation. Each case still starts
// a separate probe process against its own independently seeded database.
func legacyProbeBinary(t *testing.T, project string) string {
	t.Helper()
	root, err := filepath.Abs(project)
	if err != nil {
		t.Fatal(err)
	}
	legacyProbeBuild.once.Do(func() {
		legacyProbeBuild.root = root
		legacyProbeBuild.directory, legacyProbeBuild.err = os.MkdirTemp("", "agently-legacy-probe-")
		if legacyProbeBuild.err != nil {
			return
		}
		legacyProbeBuild.binary = filepath.Join(legacyProbeBuild.directory, "legacyprobe")
		command := exec.Command("go", "build", "-mod=mod", "-o", legacyProbeBuild.binary, ".")
		command.Dir = filepath.Join(root, "migration", "legacyprobe")
		command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
		output, err := command.CombinedOutput()
		if err != nil {
			legacyProbeBuild.err = fmt.Errorf("build isolated legacy executable: %w\n%s", err, output)
		}
	})
	if legacyProbeBuild.err != nil {
		t.Fatal(legacyProbeBuild.err)
	}
	if legacyProbeBuild.root != root {
		t.Fatalf("legacy probe requested from a different module: %s", root)
	}
	return legacyProbeBuild.binary
}

func TestMain(m *testing.M) {
	code := m.Run()
	if legacyProbeBuild.directory != "" {
		_ = os.RemoveAll(legacyProbeBuild.directory)
	}
	os.Exit(code)
}
