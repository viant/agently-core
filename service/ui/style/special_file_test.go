//go:build darwin || linux

package style

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRejectsFIFOWithoutOpeningIt(t *testing.T) {
	path := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(path, "pipe.css"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err = readFile(root, "pipe.css", MaxCSSBytes); err == nil {
		t.Fatal("FIFO accepted as stylesheet")
	}
}
