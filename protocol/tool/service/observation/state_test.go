package observation

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordReadAndVerifyCurrent(t *testing.T) {
	ctx := WithState(context.Background())
	data := []byte("hello")

	meta := RecordRead(ctx, "file://localhost/tmp/demo.txt", data, true)

	require.NotEmpty(t, meta.Token)
	assert.True(t, strings.HasPrefix(meta.Token, "sha256:"))
	assert.Equal(t, len(data), meta.Size)
	assert.True(t, meta.ContentComplete)
	assert.NoError(t, VerifyCurrent(ctx, "/tmp/demo.txt", data))
	assert.ErrorContains(t, VerifyCurrent(ctx, "/tmp/demo.txt", []byte("changed")), "changed after resources:read")
	assert.ErrorContains(t, VerifyCurrent(ctx, "/tmp/other.txt", data), "resources:read before patching")
}

func TestVerifyCurrentNoStateIsNoop(t *testing.T) {
	assert.NoError(t, VerifyCurrent(context.Background(), "/tmp/demo.txt", []byte("hello")))
}

func TestCanonicalURI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "file localhost", in: "file://localhost/tmp/root/../demo.txt", want: "/tmp/demo.txt"},
		{name: "absolute path", in: "/tmp/root/../demo.txt", want: "/tmp/demo.txt"},
		{name: "mem uri", in: "mem://localhost/root/demo.txt/", want: "mem://localhost/root/demo.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CanonicalURI(tt.in))
		})
	}
}
