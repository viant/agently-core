package patch_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/protocol/tool/service/system/patch"
)

func TestService_Snapshot_ReturnsResolvedPathsWithoutWorkdir(t *testing.T) {
	ctx := context.Background()
	workdir := t.TempDir()

	s := patch.New()
	apply, err := s.Method("apply")
	if err != nil {
		t.Fatalf("apply method: %v", err)
	}
	snapshot, err := s.Method("snapshot")
	if err != nil {
		t.Fatalf("snapshot method: %v", err)
	}
	commit, err := s.Method("commit")
	if err != nil {
		t.Fatalf("commit method: %v", err)
	}

	applyOut := &patch.ApplyOutput{}
	err = apply(ctx, &patch.ApplyInput{
		Workdir: workdir,
		Patch: `*** Begin Patch
*** Add File: nested/file.txt
+hello
*** End Patch`,
	}, applyOut)
	if err != nil {
		t.Fatalf("apply exec: %v", err)
	}
	if applyOut.Status != "ok" {
		t.Fatalf("apply status: %s error=%s", applyOut.Status, applyOut.Error)
	}

	snapshotOut := &patch.SnapshotOutput{}
	err = snapshot(ctx, &patch.EmptyInput{}, snapshotOut)
	if err != nil {
		t.Fatalf("snapshot exec: %v", err)
	}
	assert.EqualValues(t, "ok", snapshotOut.Status)
	if len(snapshotOut.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(snapshotOut.Changes))
	}

	expected := filepath.Join(workdir, "nested", "file.txt")
	assert.EqualValues(t, "create", snapshotOut.Changes[0].Kind)
	assert.EqualValues(t, expected, snapshotOut.Changes[0].URL)
	assert.EqualValues(t, "", snapshotOut.Changes[0].OrigURL)

	payload, err := json.Marshal(snapshotOut)
	if err != nil {
		t.Fatalf("marshal snapshot output: %v", err)
	}
	assert.NotContains(t, string(payload), "\"workdir\"")

	if err := commit(ctx, &patch.EmptyInput{}, &patch.EmptyOutput{}); err != nil {
		t.Fatalf("commit exec: %v", err)
	}
}
