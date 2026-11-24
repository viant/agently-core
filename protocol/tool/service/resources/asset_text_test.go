package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
)

func TestReadCodeArtifactAsBoundedOwnedText(t *testing.T) {
	ctx := assetContext(t)
	content := `{"script":"function() { throw new Error('source only'); }"}`
	uri := publishFixture(t, ctx, "source.json", "application/json", []byte(content))
	service := New(nil)
	var inspected InspectOutput
	require.NoError(t, service.inspect(ctx, &InspectInput{URI: uri}, &inspected))
	var full ReadOutput
	require.NoError(t, service.read(ctx, &ReadInput{URI: uri, ExpectedVersion: inspected.Version, MaxBytes: 4096}, &full))
	require.Equal(t, content, full.Content)
	var clipped ReadOutput
	require.NoError(t, service.read(ctx, &ReadInput{URI: uri, ExpectedVersion: inspected.Version, MaxBytes: 12}, &clipped))
	require.Equal(t, content[:12], clipped.Content)
	require.Positive(t, clipped.Remaining)
	require.Error(t, service.read(ctx, &ReadInput{URI: uri, ExpectedVersion: strings.Repeat("0", 64)}, &ReadOutput{}))
	other := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "other"})
	require.Error(t, service.read(other, &ReadInput{URI: uri}, &ReadOutput{}))
}
