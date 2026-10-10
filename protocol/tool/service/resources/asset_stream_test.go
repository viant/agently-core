package resources

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
)

type repeatedArtifactReader struct {
	pattern []byte
	offset  int
}

func (r *repeatedArtifactReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.pattern[r.offset]
		r.offset = (r.offset + 1) % len(r.pattern)
	}
	return len(p), nil
}

func TestLargeArtifactInspectionCopyAndSelectedRead(t *testing.T) {
	ctx := assetContext(t)
	const size = int64(65 << 20)
	body := io.MultiReader(strings.NewReader("id,name\n1,A\n2,B\n"), io.LimitReader(&repeatedArtifactReader{pattern: []byte("3,C\n")}, size-16))
	digest := sha256.New()
	descriptor, err := scratchpadsvc.New().PublishArtifactStream(ctx, "large.csv", "text/csv", "", io.TeeReader(body, digest))
	require.NoError(t, err)
	require.Equal(t, size, descriptor.SizeBytes)
	require.Equal(t, hex.EncodeToString(digest.Sum(nil)), descriptor.SHA256)
	service := New(nil)
	asset, err := service.loadAsset(ctx, &ReadInput{URI: descriptor.URI}, descriptor.SHA256)
	require.NoError(t, err)
	require.LessOrEqual(t, len(asset.data), 512)
	asset.close()
	var inspection InspectOutput
	require.NoError(t, service.inspect(ctx, &InspectInput{URI: descriptor.URI}, &inspection))
	require.Equal(t, int(size), inspection.SizeBytes)
	require.Equal(t, "csv", inspection.Kind)
	require.Equal(t, descriptor.SHA256, inspection.Version)
	var read ReadOutput
	require.NoError(t, service.read(ctx, &ReadInput{URI: descriptor.URI, Representation: "table", Select: &ResourceSelection{Range: "A1:B3"}}, &read))
	require.Equal(t, [][]string{{"id", "name"}, {"1", "A"}, {"2", "B"}}, read.Table.Rows)
	require.Less(t, read.Returned, 64<<10)
	require.False(t, read.Coverage.Truncated)
	require.NoError(t, service.read(ctx, &ReadInput{URI: descriptor.URI, Representation: "table", Limits: &ResourceLimits{MaxRows: 2}}, &read))
	require.Len(t, read.Table.Rows, 2)
	require.True(t, read.Coverage.Truncated)
	require.Equal(t, 3, read.Coverage.NextRow)
	require.NoError(t, service.read(ctx, &ReadInput{URI: descriptor.URI, MaxBytes: 16}, &read))
	require.LessOrEqual(t, len(read.Content), 16)
	require.Equal(t, int(size), read.Size)
	var copyOutput ExportOutput
	require.NoError(t, service.exportAsset(ctx, &ExportInput{URI: descriptor.URI, Operation: "copy", Output: ExportFormat{Format: "original"}}, &copyOutput))
	copied := copyOutput.Resources[0]
	require.NotEqual(t, descriptor.ID, copied.ID)
	require.Equal(t, descriptor.SHA256, copied.SHA256)
	require.Equal(t, descriptor.SizeBytes, copied.SizeBytes)
	_, verified, err := scratchpadsvc.New().OpenVerifiedArtifactStream(ctx, copied.URI)
	require.NoError(t, err)
	copiedHash := sha256.New()
	count, err := io.Copy(copiedHash, verified)
	require.NoError(t, err)
	require.NoError(t, verified.Close())
	require.Equal(t, size, count)
	require.Equal(t, descriptor.SHA256, hex.EncodeToString(copiedHash.Sum(nil)))
	other := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "other"})
	require.Error(t, service.inspect(other, &InspectInput{URI: descriptor.URI}, &InspectOutput{}))
	require.Error(t, service.exportAsset(other, &ExportInput{URI: descriptor.URI, Operation: "copy", Output: ExportFormat{Format: "original"}}, &ExportOutput{}))
	root := strings.ReplaceAll(os.Getenv(scratchpadsvc.EnvScratchpadURI), "${userID}", "asset-user")
	backing := filepath.Join(strings.TrimPrefix(root, "file://"), "artifacts", descriptor.ID+".bin")
	file, err := os.OpenFile(backing, os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = file.WriteAt([]byte("X"), 0)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.Error(t, service.inspect(ctx, &InspectInput{URI: descriptor.URI}, &InspectOutput{}))
	require.Error(t, service.exportAsset(ctx, &ExportInput{URI: descriptor.URI, Operation: "copy", Output: ExportFormat{Format: "original"}}, &ExportOutput{}))
}

func TestLargeWorkbookInspectAndSelectedExtractionExcludesUnrelatedSheets(t *testing.T) {
	ctx := assetContext(t)
	original := workbookFixture(t)
	archive, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	require.NoError(t, err)
	spool, err := os.CreateTemp(t.TempDir(), "large-workbook-*")
	require.NoError(t, err)
	defer spool.Close()
	writer := zip.NewWriter(spool)
	for _, file := range archive.File {
		if file.Name != "xl/worksheets/sheet2.xml" {
			require.NoError(t, writer.Copy(file))
			continue
		}
		sheet, err := writer.CreateHeader(&zip.FileHeader{Name: file.Name, Method: zip.Store})
		require.NoError(t, err)
		_, err = io.WriteString(sheet, `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><dimension ref="A1"/><sheetData>`)
		require.NoError(t, err)
		_, err = io.Copy(sheet, io.LimitReader(&repeatedArtifactReader{pattern: []byte(" ")}, 65<<20))
		require.NoError(t, err)
		_, err = io.WriteString(sheet, `</sheetData></worksheet>`)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	_, err = spool.Seek(0, io.SeekStart)
	require.NoError(t, err)
	descriptor, err := scratchpadsvc.New().PublishArtifactStream(ctx, "large.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "", spool)
	require.NoError(t, err)
	require.Greater(t, descriptor.SizeBytes, scratchpadsvc.MaxArtifactBytes)
	service := New(nil)
	var inspection InspectOutput
	require.NoError(t, service.inspect(ctx, &InspectInput{URI: descriptor.URI}, &inspection))
	require.Equal(t, "workbook", inspection.Kind)
	require.Equal(t, "Customers", inspection.Components[0].Name)
	require.Equal(t, "Orders", inspection.Components[1].Name)
	var selected InspectOutput
	require.NoError(t, service.inspect(ctx, &InspectInput{URI: descriptor.URI, Select: &ResourceSelection{ComponentID: "sheet-1"}}, &selected))
	require.Equal(t, []string{"1", "customer-1"}, selected.Columns)
	var read ReadOutput
	require.NoError(t, service.read(ctx, &ReadInput{URI: descriptor.URI, Representation: "table", Select: &ResourceSelection{Sheet: "Customers", Range: "A201:B205"}, Limits: &ResourceLimits{MaxRows: 2}}, &read))
	require.Equal(t, []int{201, 202}, read.Table.RowNumbers)
	require.Equal(t, "customer-201", read.Table.Rows[0][1])
	require.True(t, read.Coverage.Truncated)
	require.Equal(t, 203, read.Coverage.NextRow)
	require.Less(t, read.Returned, 64<<10)
}
