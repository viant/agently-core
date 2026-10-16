package model

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestModelToolPreviewLimitChecksArchitectureBeforeConversion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		bits  int
		valid bool
	}{
		{"positive32", int64(2048), 32, true}, {"negative32", int64(-1), 32, true}, {"max32", int64(2147483647), 32, true}, {"min32", int64(-2147483648), 32, true}, {"overflow32", int64(2147483648), 32, false}, {"underflow32", int64(-2147483649), 32, false}, {"native-int-overflow32", int64(1 << 40), 32, false}, {"positive-fraction", 2048.5, 64, false}, {"negative-fraction", -2048.5, 64, false}, {"string", "2048", 64, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := checkedToolPreviewLimit(tc.value, tc.bits)
			if (err == nil) != tc.valid {
				t.Fatal("integer width/type guard failed")
			}
		})
	}
}
func TestModelToolPreviewLimitRejectsFractionalAuthoredValues(t *testing.T) {
	for _, value := range []string{"2048.5", "-2048.5", "true", "'2048'", "[2048]"} {
		t.Run(value, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "model.yaml")
			if os.WriteFile(path, []byte("id: typed-preview\noptions:\n  model: test\n  toolResultPreviewLimit: "+value+"\n"), 0600) != nil {
				t.Fatal("fixture creation failed")
			}
			if _, err := New().Load(context.Background(), "file://"+path); err == nil {
				t.Fatal("invalid authored scalar accepted")
			}
		})
	}
}
