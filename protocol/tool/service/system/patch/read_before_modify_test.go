package patch

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	"github.com/viant/agently-core/protocol/tool/service/observation"
)

func TestServiceReplace_RequiresReadObservationWhenEnforced(t *testing.T) {
	ctx := observation.WithState(context.Background())
	fs := afs.New()
	workdir := "mem://localhost/rbm_replace_missing"
	target := workdir + "/file.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))
	require.NoError(t, fs.Upload(ctx, target, 0o644, strings.NewReader("before\n")))

	svc := New()
	replace, err := svc.Method("replace")
	require.NoError(t, err)

	out := &ReplaceOutput{}
	err = replace(ctx, &ReplaceInput{
		Workdir: workdir,
		Path:    "file.txt",
		Old:     "before",
		New:     "after",
	}, out)

	require.NoError(t, err)
	assert.Equal(t, "error", out.Status)
	assert.Contains(t, out.Error, "resources:read before patching")
	data, err := fs.DownloadWithURL(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "before\n", string(data))
}

func TestServiceReplace_SucceedsAfterReadObservation(t *testing.T) {
	ctx := observation.WithState(context.Background())
	fs := afs.New()
	workdir := "mem://localhost/rbm_replace_ok"
	target := workdir + "/file.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))
	require.NoError(t, fs.Upload(ctx, target, 0o644, strings.NewReader("before\n")))
	observation.RecordRead(ctx, target, []byte("before\n"), true)

	svc := New()
	replace, err := svc.Method("replace")
	require.NoError(t, err)

	out := &ReplaceOutput{}
	err = replace(ctx, &ReplaceInput{
		Workdir: workdir,
		Path:    "file.txt",
		Old:     "before",
		New:     "after",
	}, out)

	require.NoError(t, err)
	assert.Equal(t, "ok", out.Status)
	data, err := fs.DownloadWithURL(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "after\n", string(data))
}

func TestServiceReplace_FailsWhenFileChangedAfterRead(t *testing.T) {
	ctx := observation.WithState(context.Background())
	fs := afs.New()
	workdir := "mem://localhost/rbm_replace_stale"
	target := workdir + "/file.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))
	require.NoError(t, fs.Upload(ctx, target, 0o644, strings.NewReader("before\n")))
	observation.RecordRead(ctx, target, []byte("before\n"), true)
	require.NoError(t, fs.Upload(ctx, target, 0o644, strings.NewReader("external change\n")))

	svc := New()
	replace, err := svc.Method("replace")
	require.NoError(t, err)

	out := &ReplaceOutput{}
	err = replace(ctx, &ReplaceInput{
		Workdir: workdir,
		Path:    "file.txt",
		Old:     "external",
		New:     "patched",
	}, out)

	require.NoError(t, err)
	assert.Equal(t, "error", out.Status)
	assert.Contains(t, out.Error, "changed after resources:read")
	data, err := fs.DownloadWithURL(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "external change\n", string(data))
}

func TestServiceApply_UpdateRequiresReadObservationWhenEnforced(t *testing.T) {
	ctx := observation.WithState(context.Background())
	fs := afs.New()
	workdir := "mem://localhost/rbm_apply_missing"
	target := workdir + "/file.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))
	require.NoError(t, fs.Upload(ctx, target, 0o644, strings.NewReader("old\n")))

	svc := New()
	apply, err := svc.Method("apply")
	require.NoError(t, err)

	out := &ApplyOutput{}
	err = apply(ctx, &ApplyInput{
		Workdir: workdir,
		Patch: `*** Begin Patch
*** Update File: file.txt
@@
-old
+new
*** End Patch`,
	}, out)

	require.NoError(t, err)
	assert.Equal(t, "error", out.Status)
	assert.Contains(t, out.Error, "resources:read before patching")
	data, err := fs.DownloadWithURL(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "old\n", string(data))
}

func TestServiceApply_UpdateAndDeleteSucceedAfterReadObservation(t *testing.T) {
	ctx := observation.WithState(context.Background())
	fs := afs.New()
	workdir := "mem://localhost/rbm_apply_ok"
	updateTarget := workdir + "/update.txt"
	deleteTarget := workdir + "/delete.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))
	require.NoError(t, fs.Upload(ctx, updateTarget, 0o644, strings.NewReader("old\n")))
	require.NoError(t, fs.Upload(ctx, deleteTarget, 0o644, strings.NewReader("remove\n")))
	observation.RecordRead(ctx, updateTarget, []byte("old\n"), true)
	observation.RecordRead(ctx, deleteTarget, []byte("remove\n"), true)

	svc := New()
	apply, err := svc.Method("apply")
	require.NoError(t, err)

	out := &ApplyOutput{}
	err = apply(ctx, &ApplyInput{
		Workdir: workdir,
		Patch: `*** Begin Patch
*** Update File: update.txt
@@
-old
+new
*** Delete File: delete.txt
*** End Patch`,
	}, out)

	require.NoError(t, err)
	assert.Equal(t, "ok", out.Status)
	data, err := fs.DownloadWithURL(ctx, updateTarget)
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(data))
	exists, err := fs.Exists(ctx, deleteTarget)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestServiceApply_UnifiedDiffRequiresReadObservationWhenEnforced(t *testing.T) {
	ctx := observation.WithState(context.Background())
	fs := afs.New()
	workdir := "mem://localhost/rbm_unified_missing"
	target := workdir + "/file.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))
	require.NoError(t, fs.Upload(ctx, target, 0o644, strings.NewReader("old\n")))

	svc := New()
	apply, err := svc.Method("apply")
	require.NoError(t, err)

	out := &ApplyOutput{}
	err = apply(ctx, &ApplyInput{
		Workdir: workdir,
		Patch: `diff --git a/file.txt b/file.txt
--- a/file.txt
+++ b/file.txt
@@ -1 +1 @@
-old
+new`,
	}, out)

	require.NoError(t, err)
	assert.Equal(t, "error", out.Status)
	assert.Contains(t, out.Error, "resources:read before patching")
	data, err := fs.DownloadWithURL(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "old\n", string(data))
}

func TestServiceApply_UnifiedDiffSucceedsAfterReadObservation(t *testing.T) {
	ctx := observation.WithState(context.Background())
	fs := afs.New()
	workdir := "mem://localhost/rbm_unified_ok"
	target := workdir + "/file.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))
	require.NoError(t, fs.Upload(ctx, target, 0o644, strings.NewReader("old\n")))
	observation.RecordRead(ctx, target, []byte("old\n"), true)

	svc := New()
	apply, err := svc.Method("apply")
	require.NoError(t, err)

	out := &ApplyOutput{}
	err = apply(ctx, &ApplyInput{
		Workdir: workdir,
		Patch: `diff --git a/file.txt b/file.txt
--- a/file.txt
+++ b/file.txt
@@ -1 +1 @@
-old
+new`,
	}, out)

	require.NoError(t, err)
	assert.Equal(t, "ok", out.Status)
	data, err := fs.DownloadWithURL(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(data))
}

func TestServiceApply_AddDoesNotRequireReadObservation(t *testing.T) {
	ctx := observation.WithState(context.Background())
	fs := afs.New()
	workdir := "mem://localhost/rbm_apply_add"
	target := workdir + "/new.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))

	svc := New()
	apply, err := svc.Method("apply")
	require.NoError(t, err)

	out := &ApplyOutput{}
	err = apply(ctx, &ApplyInput{
		Workdir: workdir,
		Patch: `*** Begin Patch
*** Add File: new.txt
+hello
*** End Patch`,
	}, out)

	require.NoError(t, err)
	assert.Equal(t, "ok", out.Status)
	data, err := fs.DownloadWithURL(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(data))
}

func TestServiceApply_UpdateStagedAddDoesNotRequireReadObservation(t *testing.T) {
	ctx := observation.WithState(context.Background())
	fs := afs.New()
	workdir := "mem://localhost/rbm_apply_staged_add"
	target := workdir + "/new.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))

	svc := New()
	apply, err := svc.Method("apply")
	require.NoError(t, err)

	addOut := &ApplyOutput{}
	err = apply(ctx, &ApplyInput{
		Workdir: workdir,
		Patch: `*** Begin Patch
*** Add File: new.txt
+old
*** End Patch`,
	}, addOut)
	require.NoError(t, err)
	require.Equal(t, "ok", addOut.Status)

	updateOut := &ApplyOutput{}
	err = apply(ctx, &ApplyInput{
		Workdir: workdir,
		Patch: `*** Begin Patch
*** Update File: new.txt
@@
-old
+new
*** End Patch`,
	}, updateOut)

	require.NoError(t, err)
	assert.Equal(t, "ok", updateOut.Status)
	data, err := fs.DownloadWithURL(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(data))
}

func TestServiceApply_UpdateStagedExistingRequiresFreshReadObservation(t *testing.T) {
	ctx := observation.WithState(context.Background())
	fs := afs.New()
	workdir := "mem://localhost/rbm_apply_staged_existing"
	target := workdir + "/file.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))
	require.NoError(t, fs.Upload(ctx, target, 0o644, strings.NewReader("old\n")))
	observation.RecordRead(ctx, target, []byte("old\n"), true)

	svc := New()
	apply, err := svc.Method("apply")
	require.NoError(t, err)

	firstOut := &ApplyOutput{}
	err = apply(ctx, &ApplyInput{
		Workdir: workdir,
		Patch: `*** Begin Patch
*** Update File: file.txt
@@
-old
+mid
*** End Patch`,
	}, firstOut)
	require.NoError(t, err)
	require.Equal(t, "ok", firstOut.Status)

	staleOut := &ApplyOutput{}
	err = apply(ctx, &ApplyInput{
		Workdir: workdir,
		Patch: `*** Begin Patch
*** Update File: file.txt
@@
-mid
+new
*** End Patch`,
	}, staleOut)
	require.NoError(t, err)
	assert.Equal(t, "error", staleOut.Status)
	assert.Contains(t, staleOut.Error, "changed after resources:read")

	observation.RecordRead(ctx, target, []byte("mid\n"), true)
	freshOut := &ApplyOutput{}
	err = apply(ctx, &ApplyInput{
		Workdir: workdir,
		Patch: `*** Begin Patch
*** Update File: file.txt
@@
-mid
+new
*** End Patch`,
	}, freshOut)

	require.NoError(t, err)
	assert.Equal(t, "ok", freshOut.Status)
	data, err := fs.DownloadWithURL(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(data))
}

func TestServiceApply_NoObservationStatePreservesHostDirectUse(t *testing.T) {
	ctx := context.Background()
	fs := afs.New()
	workdir := "mem://localhost/rbm_no_state"
	target := workdir + "/file.txt"
	require.NoError(t, fs.Create(ctx, workdir, 0o755, true))
	require.NoError(t, fs.Upload(ctx, target, 0o644, strings.NewReader("old\n")))

	svc := New()
	apply, err := svc.Method("apply")
	require.NoError(t, err)

	out := &ApplyOutput{}
	err = apply(ctx, &ApplyInput{
		Workdir: workdir,
		Patch: `*** Begin Patch
*** Update File: file.txt
@@
-old
+new
*** End Patch`,
	}, out)

	require.NoError(t, err)
	assert.Equal(t, "ok", out.Status)
}
