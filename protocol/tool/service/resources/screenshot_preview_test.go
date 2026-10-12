package resources

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	scratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
)

func TestReadImageScreenshotPreviewPolicyPreservesOriginalAndOrdinaryImages(t *testing.T) {
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	for _, test := range []struct {
		name, role                           string
		width, height, wantWidth, wantHeight int
	}{
		{"large screenshot", "screenshot", 3000, 3000, 1600, 1600},
		{"wide screenshot", "screenshot", 3000, 1500, 2048, 1024},
		{"small screenshot", "screenshot", 320, 200, 320, 200},
		{"ordinary image", "", 3000, 3000, 768, 768},
	} {
		t.Run(test.name, func(t *testing.T) {
			var source bytes.Buffer
			require.NoError(t, png.Encode(&source, image.NewRGBA(image.Rect(0, 0, test.width, test.height))))
			original, err := scratchpad.New().PublishArtifact(ctx, "", "image.png", "image/png", "", bytes.NewReader(source.Bytes()), test.role)
			require.NoError(t, err)
			var preview ReadImageOutput
			require.NoError(t, New(nil).readImage(ctx, &ReadImageInput{URI: original.URI}, &preview))
			require.Equal(t, test.wantWidth, preview.Width)
			require.Equal(t, test.wantHeight, preview.Height)
			if test.role == "screenshot" {
				require.LessOrEqual(t, preview.Width, 2048)
				require.LessOrEqual(t, preview.Height, 2048)
				require.LessOrEqual(t, ((preview.Width+31)/32)*((preview.Height+31)/32), 2500)
			}
			descriptor, err := scratchpad.New().DescribeArtifact(ctx, preview.Encoded)
			require.NoError(t, err)
			require.Equal(t, original.URI, descriptor.SourceURI)
			require.Equal(t, test.role, descriptor.Role)
			stored, err := scratchpad.New().ReadArtifactPayload(ctx, original.URI)
			require.NoError(t, err)
			require.True(t, bytes.Equal(source.Bytes(), stored), "preview changed original artifact")
			var full ReadImageOutput
			require.NoError(t, New(nil).readImage(ctx, &ReadImageInput{URI: original.URI, Detail: "original"}, &full))
			require.Equal(t, test.width, full.Width)
			require.Equal(t, test.height, full.Height)
			fullBytes, err := scratchpad.New().ReadArtifactPayload(ctx, full.Encoded)
			require.NoError(t, err)
			require.True(t, bytes.Equal(source.Bytes(), fullBytes), "explicit original read altered pixels or encoding")
		})
	}
}
