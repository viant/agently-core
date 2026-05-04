package patch

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
)

func TestServiceReplace_StagesExactReplacement(t *testing.T) {
	ctx := context.Background()
	fs := afs.New()
	workdir := "mem://localhost/replace_exact"
	target := workdir + "/file.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))
	require.NoError(t, fs.Upload(ctx, target, 0o644, strings.NewReader("alpha\nbeta\n")))

	s := New()
	replace, err := s.Method("replace")
	require.NoError(t, err)
	snapshot, err := s.Method("snapshot")
	require.NoError(t, err)

	out := &ReplaceOutput{}
	err = replace(ctx, &ReplaceInput{
		Workdir: workdir,
		Path:    "file.txt",
		Old:     "beta",
		New:     "gamma",
	}, out)
	require.NoError(t, err)
	assert.Equal(t, "ok", out.Status)
	assert.Equal(t, target, out.Path)
	assert.Equal(t, 1, out.Replacements)
	assert.Equal(t, DiffStats{Added: 1, Removed: 1}, out.Stats)

	data, err := fs.DownloadWithURL(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "alpha\ngamma\n", string(data))

	snapshotOut := &SnapshotOutput{}
	require.NoError(t, snapshot(ctx, &EmptyInput{}, snapshotOut))
	require.Len(t, snapshotOut.Changes, 1)
	assert.Equal(t, "updated", snapshotOut.Changes[0].Kind)
	assert.Equal(t, target, snapshotOut.Changes[0].OrigURL)
	assert.Equal(t, target, snapshotOut.Changes[0].URL)
	assert.Contains(t, snapshotOut.Changes[0].Diff, "-beta")
	assert.Contains(t, snapshotOut.Changes[0].Diff, "+gamma")
}

func TestServiceReplace_RejectsAmbiguousReplacement(t *testing.T) {
	ctx := context.Background()
	fs := afs.New()
	workdir := "mem://localhost/replace_ambiguous"
	target := workdir + "/file.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))
	require.NoError(t, fs.Upload(ctx, target, 0o644, strings.NewReader("same\nsame\n")))

	s := New()
	replace, err := s.Method("replace")
	require.NoError(t, err)

	out := &ReplaceOutput{}
	err = replace(ctx, &ReplaceInput{
		Workdir: workdir,
		Path:    "file.txt",
		Old:     "same",
		New:     "changed",
	}, out)
	require.NoError(t, err)
	assert.Equal(t, "error", out.Status)
	assert.Contains(t, out.Error, "found 2 occurrences")

	data, err := fs.DownloadWithURL(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "same\nsame\n", string(data))
}

func TestServiceReplace_ReplaceAllWithExpectedOccurrences(t *testing.T) {
	ctx := context.Background()
	fs := afs.New()
	workdir := "mem://localhost/replace_all"
	target := workdir + "/file.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))
	require.NoError(t, fs.Upload(ctx, target, 0o644, strings.NewReader("one one two\n")))

	s := New()
	replace, err := s.Method("replace")
	require.NoError(t, err)

	out := &ReplaceOutput{}
	err = replace(ctx, &ReplaceInput{
		Workdir:             workdir,
		Path:                "file.txt",
		Old:                 "one",
		New:                 "three",
		ReplaceAll:          true,
		ExpectedOccurrences: 2,
	}, out)
	require.NoError(t, err)
	assert.Equal(t, "ok", out.Status)
	assert.Equal(t, 2, out.Replacements)

	data, err := fs.DownloadWithURL(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "three three two\n", string(data))
}

func TestServiceReplace_RejectsPathOutsideWorkdir(t *testing.T) {
	ctx := context.Background()
	fs := afs.New()
	workdir := "mem://localhost/replace_path"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))

	s := New()
	replace, err := s.Method("replace")
	require.NoError(t, err)

	out := &ReplaceOutput{}
	err = replace(ctx, &ReplaceInput{
		Workdir: workdir,
		Path:    "../outside.txt",
		Old:     "a",
		New:     "b",
	}, out)
	require.NoError(t, err)
	assert.Equal(t, "error", out.Status)
	assert.Contains(t, out.Error, "outside workdir")
}
