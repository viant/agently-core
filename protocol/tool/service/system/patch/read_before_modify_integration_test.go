package patch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/tool/service/observation"
	"github.com/viant/agently-core/protocol/tool/service/resources"
	"github.com/viant/agently-core/protocol/tool/service/system/patch"
	aug "github.com/viant/agently-core/service/augmenter"
)

func TestServiceApply_AcceptsResourcesReadAbsolutePathInsideWorkdir(t *testing.T) {
	ctx := observation.WithState(context.Background())
	workdir := t.TempDir()
	target := filepath.Join(workdir, "file.txt")
	require.NoError(t, os.WriteFile(target, []byte("old\n"), 0o644))

	readSvc := resources.New(aug.New(nil))
	read, err := readSvc.Method("read")
	require.NoError(t, err)
	readOut := &resources.ReadOutput{}
	err = read(ctx, &resources.ReadInput{
		URI: target,
	}, readOut)
	require.NoError(t, err)
	require.NotNil(t, readOut.Observation)

	patchSvc := patch.New()
	apply, err := patchSvc.Method("apply")
	require.NoError(t, err)

	out := &patch.ApplyOutput{}
	err = apply(ctx, &patch.ApplyInput{
		Workdir: workdir,
		Patch: "*** Begin Patch\n" +
			"*** Update File: " + target + "\n" +
			"@@\n" +
			"-old\n" +
			"+new\n" +
			"*** End Patch",
	}, out)

	require.NoError(t, err)
	assert.Equal(t, "ok", out.Status)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(data))
}
