package resources

import (
	"bytes"
	"context"
	"encoding/csv"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/stretchr/testify/require"

	authctx "github.com/viant/agently-core/internal/auth"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	"github.com/xuri/excelize/v2"
)

func assetContext(t *testing.T) context.Context {
	t.Helper()
	t.Setenv(scratchpadsvc.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	return authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "asset-user"})
}
func publishFixture(t *testing.T, ctx context.Context, name, mime string, data []byte) string {
	t.Helper()
	d, err := scratchpadsvc.New().PublishArtifact(ctx, "", name, mime, "", bytes.NewReader(data))
	require.NoError(t, err)
	return d.URI
}
func workbookFixture(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	require.NoError(t, f.SetSheetName("Sheet1", "Customers"))
	_, err := f.NewSheet("Orders")
	require.NoError(t, err)
	for i := 1; i <= 205; i++ {
		cell, _ := excelize.CoordinatesToCellName(1, i)
		row := []string{strconv.Itoa(i), "customer-" + strconv.Itoa(i)}
		require.NoError(t, f.SetSheetRow("Customers", cell, &row))
	}
	require.NoError(t, f.SetCellValue("Orders", "A1", "order"))
	b, err := f.WriteToBuffer()
	require.NoError(t, err)
	return b.Bytes()
}
func pdfFixture(t *testing.T) []byte {
	t.Helper()
	f := fpdf.New("P", "mm", "A4", "")
	f.AddPage()
	f.SetFont("Arial", "", 12)
	f.Cell(40, 10, "First page")
	f.AddPage()
	f.Cell(40, 10, "Second page")
	var b bytes.Buffer
	require.NoError(t, f.Output(&b))
	return b.Bytes()
}
func TestAbsoluteResourceResolution(t *testing.T) {
	ctx := assetContext(t)
	s := New(nil)
	uri := publishFixture(t, ctx, "a.txt", "text/plain", []byte("hello"))
	for _, allowed := range [][]string{nil, {"file:///unrelated/knowledge"}} {
		got, err := s.normalizeFullURI(ctx, uri, allowed)
		require.NoError(t, err)
		require.Equal(t, uri, got)
		target, err := s.resolveReadTarget(ctx, &ReadInput{URI: uri, RootID: "nonexistent"}, allowed)
		require.NoError(t, err)
		require.Equal(t, uri, target.fullURI)
	}
	file := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(file, []byte("outside"), 0600))
	for _, in := range []*ReadInput{{URI: file}, {URI: "file://" + file}, {Path: file, RootID: "nonexistent"}} {
		target, err := s.resolveReadTarget(ctx, in, []string{"file:///unrelated"})
		require.NoError(t, err)
		data, err := s.downloadResource(ctx, target.fullURI)
		require.NoError(t, err)
		require.Equal(t, "outside", string(data))
	}
	for _, uri := range []string{"https://example.test/a", "https://bad%url", "s3://bucket/a", "scratchpad://note/a", "scratchpad://artifact/a%2fb"} {
		_, err := s.normalizeFullURI(ctx, uri, nil)
		require.Error(t, err, uri)
	}
	_, err := s.resolveReadTarget(ctx, &ReadInput{Path: "relative.txt"}, nil)
	require.Error(t, err)
	var out ReadOutput
	require.NoError(t, s.read(ctx, &ReadInput{URI: uri}, &out))
	require.Equal(t, "hello", out.Content)
	other := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "other"})
	require.Error(t, s.read(other, &ReadInput{URI: uri}, &ReadOutput{}))
	require.False(t, isAllowedWorkspace("file:///tmp/knowledge-private/a", []string{"file:///tmp/knowledge"}))
}
func TestInspectWorkbookAndReadPagination(t *testing.T) {
	ctx := assetContext(t)
	uri := publishFixture(t, ctx, "book.xlsx", "application/octet-stream", workbookFixture(t))
	s := New(nil)
	var meta InspectOutput
	require.NoError(t, s.inspect(ctx, &InspectInput{URI: uri, Limit: 1}, &meta))
	require.Equal(t, "workbook", meta.Kind)
	require.False(t, meta.Complete)
	require.Equal(t, "Customers", meta.Components[0].Name)
	var next InspectOutput
	require.NoError(t, s.inspect(ctx, &InspectInput{URI: uri, Cursor: meta.NextCursor}, &next))
	require.Equal(t, "Orders", next.Components[0].Name)
	require.True(t, next.Complete)
	require.Error(t, s.inspect(ctx, &InspectInput{URI: uri, Cursor: "bad"}, &next))
	var detail InspectOutput
	require.NoError(t, s.inspect(ctx, &InspectInput{URI: uri, Select: &ResourceSelection{ComponentID: "sheet-1"}}, &detail))
	require.Equal(t, []string{"1", "customer-1"}, detail.Columns)
	input := &ReadInput{URI: uri, Representation: "table", Select: &ResourceSelection{Sheet: "Customers", Range: "A1:B205"}, Limits: &ResourceLimits{MaxRows: 100}, ExpectedVersion: meta.Version}
	var read ReadOutput
	require.NoError(t, s.read(ctx, input, &read))
	require.Len(t, read.Table.Rows, 100)
	require.True(t, read.Coverage.Truncated)
	require.Equal(t, 101, read.Coverage.NextRow)
	input.Cursor = read.Cursor
	require.NoError(t, s.read(ctx, input, &read))
	require.Equal(t, 101, read.Table.RowNumbers[0])
	require.Len(t, read.Table.Rows, 100)
	input.Cursor = read.Cursor
	require.NoError(t, s.read(ctx, input, &read))
	require.Len(t, read.Table.Rows, 5)
	require.False(t, read.Coverage.Truncated)
	input.ExpectedVersion = "changed"
	require.ErrorContains(t, s.read(ctx, input, &read), "resource_changed")
	for _, sel := range []*ResourceSelection{nil, {Sheet: "missing"}, {Sheet: "Orders", ComponentID: "sheet-1"}, {Sheet: "Customers", Range: "B5:A1"}, {Sheet: "Customers", Pages: []int{1}}} {
		require.Error(t, s.read(ctx, &ReadInput{URI: uri, Representation: "table", Select: sel}, &ReadOutput{}))
	}
}
func TestExportCompleteWorkbookSelection(t *testing.T) {
	ctx := assetContext(t)
	s := New(nil)
	uri := publishFixture(t, ctx, "book.xlsx", "application/octet-stream", workbookFixture(t))
	for _, format := range []string{"csv", "json", "xlsx"} {
		var out ExportOutput
		require.NoError(t, s.exportAsset(ctx, &ExportInput{URI: uri, Select: &ResourceSelection{Sheet: "Customers"}, Operation: "convert", Output: ExportFormat{Format: format}}, &out))
		require.True(t, out.Complete)
		require.Len(t, out.Resources, 1)
		_, r, err := scratchpadsvc.New().OpenArtifact(ctx, out.Resources[0].URI)
		require.NoError(t, err)
		data, err := io.ReadAll(r)
		r.Close()
		require.NoError(t, err)
		if format == "csv" {
			rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
			require.NoError(t, err)
			require.Len(t, rows, 205)
			require.Equal(t, "205", rows[204][0])
		}
		if format == "json" {
			require.Contains(t, string(data), "customer-205")
		}
		if format == "xlsx" {
			f, err := excelize.OpenReader(bytes.NewReader(data))
			require.NoError(t, err)
			v, err := f.GetCellValue("Sheet1", "B205")
			f.Close()
			require.NoError(t, err)
			require.Equal(t, "customer-205", v)
		}
	}
}
func TestPDFTextNativeAndExport(t *testing.T) {
	ctx := assetContext(t)
	s := New(nil)
	data := pdfFixture(t)
	uri := publishFixture(t, ctx, "report.pdf", "application/pdf", data)
	var meta InspectOutput
	require.NoError(t, s.inspect(ctx, &InspectInput{URI: uri}, &meta))
	require.Equal(t, 2, meta.PageCount)
	var out ReadOutput
	require.NoError(t, s.read(ctx, &ReadInput{URI: uri, Representation: "text", Select: &ResourceSelection{Pages: []int{2}}}, &out))
	require.Contains(t, out.Content, "Second page")
	require.NotContains(t, out.Content, "First page")
	require.Error(t, s.read(ctx, &ReadInput{URI: uri, Representation: "text", Select: &ResourceSelection{Pages: []int{3}}}, &out))
	require.ErrorContains(t, s.read(ctx, &ReadInput{URI: uri, Representation: "text", Options: &ResourceOptions{OCR: "auto"}}, &out), "OCR")
	var native ReadOutput
	require.NoError(t, s.read(ctx, &ReadInput{URI: uri, Representation: "native"}, &native))
	require.NotNil(t, native.Native)
	require.Empty(t, native.Content)
	_, r, err := scratchpadsvc.New().OpenArtifact(ctx, native.Native.URI)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	require.Equal(t, data, got)
	require.Error(t, s.read(ctx, &ReadInput{URI: uri, Representation: "native", Select: &ResourceSelection{Pages: []int{1}}}, &out))
	var exported ExportOutput
	require.NoError(t, s.exportAsset(ctx, &ExportInput{URI: uri, Select: &ResourceSelection{Pages: []int{1}}, Operation: "convert", Output: ExportFormat{Format: "txt"}}, &exported))
	require.Len(t, exported.Resources, 1)
}
func TestReadImageFromScratchpad(t *testing.T) {
	ctx := assetContext(t)
	s := New(nil)
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 20, 10))))
	uri := publishFixture(t, ctx, "image.png", "image/png", b.Bytes())
	for _, include := range []bool{false, true} {
		var out ReadImageOutput
		require.NoError(t, s.readImage(ctx, &ReadImageInput{URI: uri, IncludeData: include}, &out))
		require.Equal(t, "image.png", out.Name)
		require.Equal(t, 20, out.Width)
		require.Equal(t, 10, out.Height)
		if include {
			require.NotEmpty(t, out.Base64)
		} else {
			require.Empty(t, out.Base64)
		}
		require.True(t, strings.HasPrefix(out.Encoded, "scratchpad://artifact/"))
	}
	require.Error(t, s.readImage(ctx, &ReadImageInput{URI: uri, MaxWidth: 5000}, &ReadImageOutput{}))
	require.Error(t, s.readImage(ctx, &ReadImageInput{URI: uri, DestURL: "file:///untrusted/output"}, &ReadImageOutput{}))
	var exported ExportOutput
	require.NoError(t, s.exportAsset(ctx, &ExportInput{URI: uri, Operation: "convert", Output: ExportFormat{Format: "jpeg"}}, &exported))
	require.Equal(t, "image/jpeg", exported.Resources[0].MimeType)
}
func TestCSVLimitsAndCursorSelection(t *testing.T) {
	ctx := assetContext(t)
	s := New(nil)
	uri := publishFixture(t, ctx, "a.csv", "text/csv", []byte("id,name\n1,A\n2,B\n"))
	var out ReadOutput
	require.NoError(t, s.read(ctx, &ReadInput{URI: uri, Representation: "table", Limits: &ResourceLimits{MaxRows: 1}}, &out))
	require.Len(t, out.Table.Rows, 1)
	require.Error(t, s.read(ctx, &ReadInput{URI: uri, Representation: "table", Cursor: out.Cursor, Select: &ResourceSelection{Range: "A2:B3"}}, &out))
	huge := publishFixture(t, ctx, "huge.csv", "text/csv", []byte(strings.Repeat("x", 70000)+"\n"))
	require.ErrorContains(t, s.read(ctx, &ReadInput{URI: huge, Representation: "table"}, &out), "output limit")
	require.Error(t, s.read(ctx, &ReadInput{URI: uri, Representation: "table", Options: &ResourceOptions{Values: "formula"}}, &out))
}
func TestMutableResourceVersion(t *testing.T) {
	ctx := assetContext(t)
	s := New(nil)
	file := filepath.Join(t.TempDir(), "data.csv")
	require.NoError(t, os.WriteFile(file, []byte("a\n1\n"), 0600))
	var meta InspectOutput
	require.NoError(t, s.inspect(ctx, &InspectInput{URI: file}, &meta))
	require.NoError(t, os.WriteFile(file, []byte("a\n2\n"), 0600))
	require.ErrorContains(t, s.read(ctx, &ReadInput{URI: file, Representation: "table", ExpectedVersion: meta.Version}, &ReadOutput{}), "resource_changed")
	require.ErrorContains(t, s.read(ctx, &ReadInput{URI: file, ExpectedVersion: meta.Version}, &ReadOutput{}), "resource_changed")
	require.ErrorContains(t, s.exportAsset(ctx, &ExportInput{URI: file, Operation: "convert", Output: ExportFormat{Format: "csv"}, ExpectedVersion: meta.Version}, &ExportOutput{}), "resource_changed")
}

func TestListUserArtifactsAndAbsoluteDirectory(t *testing.T) {
	ctx := assetContext(t)
	s := New(nil)
	for i := 0; i < 3; i++ {
		publishFixture(t, ctx, "file.txt", "text/plain", []byte(strconv.Itoa(i)))
	}
	var out ListOutput
	require.NoError(t, s.list(ctx, &ListInput{Scope: "artifacts", MaxItems: 2}, &out))
	require.Len(t, out.Items, 2)
	require.NotEmpty(t, out.NextCursor)
	var next ListOutput
	require.NoError(t, s.list(ctx, &ListInput{Scope: "artifacts", Cursor: out.NextCursor}, &next))
	require.Len(t, next.Items, 1)
	other := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "other"})
	require.NoError(t, s.list(other, &ListInput{Scope: "artifacts"}, &next))
	require.Empty(t, next.Items)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0600))
	require.NoError(t, s.list(ctx, &ListInput{Path: dir, RootID: "does-not-exist"}, &next))
	require.Len(t, next.Items, 1)
}
func TestRelativeResourceContainment(t *testing.T) {
	ctx := assetContext(t)
	s := New(nil)
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "x"), []byte("x"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))
	for _, relative := range []string{"../escape", "link/x"} {
		_, err := s.resolveReadTarget(ctx, &ReadInput{RootURI: "file://" + root, Path: relative}, nil)
		require.Error(t, err)
	}
	// Absolute inputs intentionally bypass resource-root containment.
	_, err := s.resolveReadTarget(ctx, &ReadInput{RootURI: "file://" + root, Path: filepath.Join(outside, "x")}, nil)
	require.NoError(t, err)
}

func TestPDFMediaBackendAvailabilityAndValidation(t *testing.T) {
	ctx := assetContext(t)
	s := New(nil)
	uri := publishFixture(t, ctx, "a.pdf", "application/pdf", pdfFixture(t))
	t.Setenv("PATH", t.TempDir())
	var meta InspectOutput
	require.NoError(t, s.inspect(ctx, &InspectInput{URI: uri}, &meta))
	require.NotContains(t, meta.Capabilities, "render")
	require.NotContains(t, meta.Capabilities, "extractImages")
	for _, operation := range []string{"render", "extractImages"} {
		err := s.exportAsset(ctx, &ExportInput{URI: uri, Select: &ResourceSelection{Pages: []int{1}}, Operation: operation, Output: ExportFormat{Format: "png"}}, &ExportOutput{})
		require.ErrorContains(t, err, "backend unavailable")
	}
	require.Error(t, s.exportAsset(ctx, &ExportInput{URI: uri, Operation: "render", Output: ExportFormat{Format: "png"}}, &ExportOutput{}))
}
func TestPDFRenderWhenBackendInstalled(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("optional pdftoppm backend not installed")
	}
	ctx := assetContext(t)
	s := New(nil)
	uri := publishFixture(t, ctx, "a.pdf", "application/pdf", pdfFixture(t))
	var out ExportOutput
	require.NoError(t, s.exportAsset(ctx, &ExportInput{URI: uri, Select: &ResourceSelection{Pages: []int{1}}, Operation: "render", Output: ExportFormat{Format: "png", DPI: 72}}, &out))
	require.True(t, out.Complete)
	require.Len(t, out.Resources, 1)
	_, r, err := scratchpadsvc.New().OpenArtifact(ctx, out.Resources[0].URI)
	require.NoError(t, err)
	defer r.Close()
	config, format, err := image.DecodeConfig(r)
	require.NoError(t, err)
	require.Equal(t, "png", format)
	require.LessOrEqual(t, config.Width, 4096)
	require.LessOrEqual(t, config.Height, 4096)
}
func TestAssetCanceledAndInputLimit(t *testing.T) {
	ctx := assetContext(t)
	uri := publishFixture(t, ctx, "a.csv", "text/csv", []byte("id\n1\n"))
	s := New(nil)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.Error(t, s.inspect(canceled, &InspectInput{URI: uri}, &InspectOutput{}))
	require.Error(t, s.read(canceled, &ReadInput{URI: uri, Representation: "table"}, &ReadOutput{}))
	require.Error(t, s.exportAsset(canceled, &ExportInput{URI: uri, Operation: "convert", Output: ExportFormat{Format: "csv"}}, &ExportOutput{}))
	file := filepath.Join(t.TempDir(), "oversize")
	f, err := os.Create(file)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(scratchpadsvc.MaxArtifactBytes+1))
	require.NoError(t, f.Close())
	_, err = s.downloadResource(ctx, "file://"+file)
	require.ErrorContains(t, err, "input byte limit")
}
