package resources

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/tool/service/observation"
	aug "github.com/viant/agently-core/service/augmenter"
)

func TestServiceRead_EmitsAndRecordsObservation(t *testing.T) {
	ctx := observation.WithState(context.Background())
	svc := New(aug.New(nil))
	rootURI := tempDirURL(t)
	fullPath := writeFile(t, rootURI, "note.txt", "hello observation")

	out := &ReadOutput{}
	err := svc.read(ctx, &ReadInput{
		RootURI: rootURI,
		Path:    "note.txt",
	}, out)

	require.NoError(t, err)
	require.NotNil(t, out.Observation)
	assert.Equal(t, "hello observation", out.Content)
	assert.Equal(t, len("hello observation"), out.Observation.Size)
	assert.True(t, out.Observation.ContentComplete)
	assert.NotEmpty(t, out.Observation.SHA256)
	assert.Equal(t, "sha256:"+out.Observation.SHA256, out.Observation.Token)
	assert.NoError(t, observation.VerifyCurrent(ctx, fullPath, []byte("hello observation")))
}

func TestServiceRead_ObservationHashCoversFullResourceWhenOutputClipped(t *testing.T) {
	ctx := observation.WithState(context.Background())
	svc := New(aug.New(nil))
	rootURI := tempDirURL(t)
	fullPath := writeFile(t, rootURI, "large.txt", "abcdef")

	out := &ReadOutput{}
	err := svc.read(ctx, &ReadInput{
		RootURI:  rootURI,
		Path:     "large.txt",
		MaxBytes: 3,
	}, out)

	require.NoError(t, err)
	require.NotNil(t, out.Observation)
	assert.Equal(t, "abc", out.Content)
	assert.Equal(t, 6, out.Observation.Size)
	assert.False(t, out.Observation.ContentComplete)
	assert.NoError(t, observation.VerifyCurrent(ctx, fullPath, []byte("abcdef")))
	assert.ErrorContains(t, observation.VerifyCurrent(ctx, fullPath, []byte("abc")), "changed after resources:read")
}
