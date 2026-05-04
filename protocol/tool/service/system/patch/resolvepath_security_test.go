package patch

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/afs"
)

func TestResolvePath_Workdir_NoBypass(t *testing.T) {
	base := string(filepath.Separator) + filepath.Join("workspace", "project")

	testCases := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{
			name: "relative path stays under workdir",
			path: "a/b.txt",
			want: filepath.Join(base, "a", "b.txt"),
		},
		{
			name: "relative path can normalize without escaping",
			path: "a/../b.txt",
			want: filepath.Join(base, "b.txt"),
		},
		{
			name: "absolute path inside workdir is accepted",
			path: filepath.Join(base, "a", "b.txt"),
			want: filepath.Join(base, "a", "b.txt"),
		},
		{
			name:    "absolute sibling prefix is rejected",
			path:    string(filepath.Separator) + filepath.Join("workspace", "project2", "file.txt"),
			wantErr: true,
		},
		{
			name:    "absolute path outside workdir is rejected",
			path:    string(filepath.Separator) + filepath.Join("etc", "passwd"),
			wantErr: true,
		},
		{
			name:    "path traversal is rejected",
			path:    "../secrets.txt",
			wantErr: true,
		},
		{
			name:    "nested traversal is rejected",
			path:    "a/../../secrets.txt",
			wantErr: true,
		},
		{
			name:    "reentering traversal is rejected",
			path:    filepath.ToSlash(filepath.Join("..", "..", "workspace", "project", "reentered.txt")),
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolvePath(tc.path, base)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.True(t, containsFilesystemPath(base, got), "resolved path must stay under workdir")
		})
	}
}

func TestResolvePath_URLWorkdir_NoBypass(t *testing.T) {
	workdir := "mem://localhost/work"

	testCases := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{
			name: "relative path stays under URL workdir",
			path: "a/b.txt",
			want: "mem://localhost/work/a/b.txt",
		},
		{
			name: "relative URL path can normalize without escaping",
			path: "a/../b.txt",
			want: "mem://localhost/work/b.txt",
		},
		{
			name: "absolute URL path inside workdir is accepted",
			path: "mem://localhost/work/a/b.txt",
			want: "mem://localhost/work/a/b.txt",
		},
		{
			name:    "absolute URL sibling prefix is rejected",
			path:    "mem://localhost/work2/a.txt",
			wantErr: true,
		},
		{
			name:    "URL path traversal is rejected",
			path:    "../outside.txt",
			wantErr: true,
		},
		{
			name:    "reentering URL traversal is rejected",
			path:    "../work/reentered.txt",
			wantErr: true,
		},
		{
			name:    "filesystem absolute path is rejected for non-file URL workdir",
			path:    "/work/a.txt",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolvePath(tc.path, workdir)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestServiceApply_TraversalCantWriteOutsideWorkdir(t *testing.T) {
	fs := afs.New()
	ctx := context.Background()

	workdir := "mem://localhost/work"
	outside := "mem://localhost/outside.txt"

	// Ensure workdir exists.
	assert.NoError(t, fs.Create(ctx, workdir, 0o755, true))

	s := New()

	// Attempt to write outside of workdir via traversal.
	patchText := `*** Begin Patch
*** Add File: ../outside.txt
+pwn
*** End Patch`

	var out ApplyOutput
	err := s.applyPatch(ctx, &ApplyInput{Patch: patchText, Workdir: workdir}, &out)
	assert.Error(t, err)

	exists, err := fs.Exists(ctx, outside)
	assert.NoError(t, err)
	assert.False(t, exists, "should not be able to write outside the workdir")

	exists, err = fs.Exists(ctx, "mem://localhost/work/outside.txt")
	assert.NoError(t, err)
	assert.False(t, exists, "should not rewrite traversal to a different in-workdir file")
}

func TestServiceApply_AbsolutePathInsideWorkdir(t *testing.T) {
	fs := afs.New()
	ctx := context.Background()

	workdir := "mem://localhost/work"
	target := "mem://localhost/work/inside.txt"

	assert.NoError(t, fs.Create(ctx, workdir, 0o755, true))

	s := New()
	patchText := `*** Begin Patch
*** Add File: mem://localhost/work/inside.txt
+ok
*** End Patch`

	var out ApplyOutput
	err := s.applyPatch(ctx, &ApplyInput{Patch: patchText, Workdir: workdir}, &out)
	assert.NoError(t, err)

	exists, err := fs.Exists(ctx, target)
	assert.NoError(t, err)
	assert.True(t, exists)
}
