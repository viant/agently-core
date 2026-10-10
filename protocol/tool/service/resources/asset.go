package resources

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	"github.com/xuri/excelize/v2"
)

const maxResourceRows = 100000
const maxResourceCells = 1000000
const maxReadOutputBytes = 64 << 10

type asset struct {
	uri, name, mime, kind, version string
	data                           []byte
	stream                         io.ReadSeekCloser
	size                           int64
}

func (s *Service) loadAsset(ctx context.Context, in *ReadInput, expected string) (*asset, error) {
	target, err := s.resolveReadTarget(ctx, in, s.agentAllowed(ctx))
	if err != nil {
		return nil, err
	}
	a := &asset{uri: target.fullURI, name: path.Base(target.fullURI)}
	if strings.HasPrefix(a.uri, "scratchpad://") {
		d, reader, e := scratchpadsvc.New().OpenVerifiedArtifactStream(ctx, a.uri)
		if e != nil {
			return nil, e
		}
		a.name, a.mime, a.version, a.size, a.stream = d.Name, d.MimeType, d.SHA256, d.SizeBytes, reader
		a.data = make([]byte, 512)
		n, e := io.ReadFull(reader, a.data)
		if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
			reader.Close()
			return nil, e
		}
		a.data = a.data[:n]
		_, err = reader.Seek(0, io.SeekStart)
	} else {
		a.data, err = s.downloadResource(ctx, a.uri)
		hash := sha256.Sum256(a.data)
		a.version = hex.EncodeToString(hash[:])
		a.size = int64(len(a.data))
	}
	if err != nil {
		a.close()
		return nil, err
	}
	if expected != "" && expected != a.version {
		a.close()
		return nil, fmt.Errorf("resource_changed")
	}
	a.kind = "binary"
	switch {
	case bytes.HasPrefix(a.data, []byte("%PDF-")):
		a.kind = "pdf"
		a.mime = "application/pdf"
	case bytes.HasPrefix(a.data, []byte("PK")):
		_, e := a.workbookMetadata()
		if e == nil {
			a.kind = "workbook"
			a.mime = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		}
	default:
		source, sourceErr := a.reader()
		if sourceErr != nil {
			a.close()
			return nil, sourceErr
		}
		if _, _, e := image.DecodeConfig(io.LimitReader(source, scratchpadsvc.MaxArtifactBytes)); e == nil {
			a.kind = "image"
			a.mime = http.DetectContentType(a.data)
		} else if validTextPrefix(a.data, a.stream != nil && a.size > int64(len(a.data))) {
			declaredMime := a.mime
			a.kind = "text"
			a.mime = "text/plain"
			if strings.EqualFold(path.Ext(a.name), ".csv") || strings.Contains(declaredMime, "csv") {
				a.kind = "csv"
				a.mime = "text/csv"
			}
		}
	}
	if a.mime == "" {
		a.mime = http.DetectContentType(a.data)
	}
	return a, nil
}
func (a *asset) close() {
	if a.stream != nil {
		_ = a.stream.Close()
	}
}
func (a *asset) reader() (io.Reader, error) {
	if a.stream != nil {
		_, err := a.stream.Seek(0, io.SeekStart)
		return a.stream, err
	}
	return bytes.NewReader(a.data), nil
}
func (a *asset) materialize() error {
	if a.stream == nil {
		return nil
	}
	if a.size > scratchpadsvc.MaxArtifactBytes {
		return fmt.Errorf("resource input byte limit exceeded")
	}
	reader, err := a.reader()
	if err != nil {
		return err
	}
	a.data, err = io.ReadAll(io.LimitReader(reader, scratchpadsvc.MaxArtifactBytes+1))
	return err
}
func (a *asset) workbook(selection *ResourceSelection) (*excelize.File, error) {
	if a.stream != nil {
		return a.selectedWorkbook(selection)
	}
	return excelize.OpenReader(bytes.NewReader(a.data), excelize.Options{UnzipSizeLimit: scratchpadsvc.MaxArtifactBytes, UnzipXMLSizeLimit: 8 << 20})
}
func selectSheet(f *excelize.File, sel *ResourceSelection) (string, error) {
	names := f.GetSheetList()
	name := ""
	if sel != nil {
		name = sel.Sheet
		if sel.ComponentID != "" {
			i, e := strconv.Atoi(strings.TrimPrefix(sel.ComponentID, "sheet-"))
			if e != nil || i < 1 || i > len(names) || !strings.HasPrefix(sel.ComponentID, "sheet-") {
				return "", fmt.Errorf("unknown sheet component")
			}
			if name != "" && name != names[i-1] {
				return "", fmt.Errorf("conflicting sheet selectors")
			}
			name = names[i-1]
		}
		if len(sel.Pages) > 0 {
			return "", fmt.Errorf("page selector is not valid for workbook")
		}
	}
	if name == "" {
		if len(names) != 1 {
			return "", fmt.Errorf("select a sheet; inspect the workbook first")
		}
		name = names[0]
	}
	for _, n := range names {
		if n == name {
			return name, nil
		}
	}
	return "", fmt.Errorf("unknown sheet %q", name)
}
func (s *Service) inspect(ctx context.Context, in, out interface{}) error {
	req, ok := in.(*InspectInput)
	if !ok {
		return fmt.Errorf("invalid inspect input")
	}
	result, ok := out.(*InspectOutput)
	if !ok {
		return fmt.Errorf("invalid inspect output")
	}
	a, err := s.loadAsset(ctx, &ReadInput{URI: req.URI, Path: req.Path, RootID: req.RootID}, req.ExpectedVersion)
	if err != nil {
		return err
	}
	defer a.close()
	*result = InspectOutput{URI: a.uri, Name: a.name, MimeType: a.mime, Kind: a.kind, SizeBytes: int(a.size), Version: a.version, Capabilities: map[string][]string{}, Complete: true, NativeRequiresProviderSupport: true}
	switch a.kind {
	case "workbook":
		sheets, e := a.workbookMetadata()
		if e != nil {
			return e
		}
		result.Capabilities["read"] = []string{"table", "native"}
		result.Capabilities["export"] = []string{"csv", "json", "xlsx"}
		for i, sheet := range sheets {
			result.Components = append(result.Components, ResourceComponent{ID: fmt.Sprintf("sheet-%d", i+1), Name: sheet.name, Kind: "sheet", UsedRange: sheet.dimension, UsedRangeEstimated: true})
		}
		if req.Select != nil {
			f, e := a.workbook(req.Select)
			if e != nil {
				return e
			}
			defer f.Close()
			name, e := selectSheet(f, req.Select)
			if e != nil {
				return e
			}
			rows, e := f.Rows(name)
			if e != nil {
				return e
			}
			defer rows.Close()
			if rows.Next() {
				result.ColumnsInferred = true
				result.Columns, e = rows.Columns()
				if e != nil {
					return e
				}
				if len(result.Columns) > 128 {
					result.Columns = result.Columns[:128]
				}
				for i, v := range result.Columns {
					if len(v) > 256 {
						result.Columns[i] = v[:256]
					}
				}
			}
		}
	case "pdf":
		r, e := a.pdfReader()
		if e != nil {
			return e
		}
		result.PageCount = r.NumPage()
		if result.PageCount > 10000 {
			return fmt.Errorf("PDF page count exceeds inspection limit")
		}
		result.Capabilities["read"] = []string{"text", "native"}
		result.Capabilities["export"] = []string{"txt"}
		if _, e := exec.LookPath("pdftoppm"); e == nil {
			result.Capabilities["render"] = []string{"png"}
		}
		if _, e := exec.LookPath("pdfimages"); e == nil {
			result.Capabilities["extractImages"] = []string{"original", "png"}
		}
		for i := 1; i <= r.NumPage() && i <= 10000; i++ {
			result.Components = append(result.Components, ResourceComponent{ID: fmt.Sprintf("page-%d", i), Name: strconv.Itoa(i), Kind: "page"})
		}
	case "image":
		source, e := a.reader()
		if e != nil {
			return e
		}
		c, _, e := image.DecodeConfig(source)
		if e != nil {
			return e
		}
		result.Width = c.Width
		result.Height = c.Height
		result.Capabilities["readImage"] = []string{"image"}
		result.Capabilities["export"] = []string{"png", "jpeg"}
	case "csv":
		result.Capabilities["read"] = []string{"text", "table", "native"}
		result.Capabilities["export"] = []string{"csv", "json"}
	case "text":
		result.Capabilities["read"] = []string{"text", "native"}
		result.Capabilities["export"] = []string{"txt"}
	}
	offset := 0
	if req.Cursor != "" {
		parts := strings.Split(req.Cursor, ":")
		if len(parts) != 2 || parts[0] != a.version {
			return fmt.Errorf("invalid inspection cursor")
		}
		offset, err = strconv.Atoi(parts[1])
		if err != nil {
			return fmt.Errorf("invalid inspection cursor")
		}
	}
	if offset < 0 || offset > len(result.Components) {
		return fmt.Errorf("invalid inspection cursor")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	end := offset + limit
	if end > len(result.Components) {
		end = len(result.Components)
	}
	if end < len(result.Components) {
		result.Complete = false
		result.NextCursor = a.version + ":" + strconv.Itoa(end)
	}
	result.Components = result.Components[offset:end]
	return ctx.Err()
}

func (s *Service) readAsset(ctx context.Context, input *ReadInput, output *ReadOutput) error {
	a, err := s.loadAsset(ctx, input, input.ExpectedVersion)
	if err != nil {
		return err
	}
	defer a.close()
	if a.kind == "workbook" {
		output.Warnings = []string{"Formula values use workbook caches; formulas are not recalculated."}
	}
	output.URI = a.uri
	output.Size = int(a.size)
	output.Version = a.version
	rep := input.Representation
	if rep == "native" {
		if input.Select != nil || input.Options != nil || input.Cursor != "" {
			return fmt.Errorf("native read requires the whole resource; export a selection first")
		}
		reader, e := a.reader()
		if e != nil {
			return e
		}
		d, e := scratchpadsvc.New().PublishArtifactStream(ctx, a.name, a.mime, a.uri, reader)
		if e != nil {
			return e
		}
		output.Native = &NativeResource{URI: d.URI, Name: d.Name, MimeType: d.MimeType, SHA256: d.SHA256}
		return nil
	}
	maxBytes := 32768
	maxRows := 100
	if input.Limits != nil {
		if input.Limits.MaxOutputBytes > 0 {
			maxBytes = input.Limits.MaxOutputBytes
		}
		if input.Limits.MaxRows > 0 {
			maxRows = input.Limits.MaxRows
		}
	}
	if maxBytes > maxReadOutputBytes {
		maxBytes = maxReadOutputBytes
	}
	if maxRows > 1000 {
		maxRows = 1000
	}
	if rep == "table" {
		start := 0
		var e error
		if input.Cursor != "" {
			start, e = decodeReadCursor(input.Cursor, a.version, input.Select, input.Options)
			if e != nil {
				return e
			}
		}
		if start < 0 {
			return fmt.Errorf("invalid read cursor")
		}
		output.Coverage = &ResourceCoverage{Selection: input.Select}
		table, e := a.tablePage(ctx, input.Select, input.Options, start, maxRows, maxBytes, output.Coverage)
		if e != nil {
			return e
		}
		if output.Coverage.Truncated {
			output.Cursor = encodeReadCursor(a.version, input.Select, input.Options, start+len(table.Rows))
		} else {
			output.Cursor = ""
		}
		output.Table = table
		output.Returned = 0
		for _, row := range table.Rows {
			output.Returned += tableRowBytes(row)
		}
		return nil
	}
	if rep != "text" {
		return fmt.Errorf("unsupported representation %q", rep)
	}
	if a.stream != nil && (a.kind == "text" || a.kind == "csv") {
		if input.Select != nil {
			return fmt.Errorf("use byte/line ranges for text")
		}
		return a.readStreamText(ctx, input, output, maxBytes)
	}
	text, e := a.text(ctx, input.Select, input.Options)
	if e != nil {
		return e
	}
	selection, e := applyReadSelection([]byte(text), &ReadInput{MaxBytes: maxBytes, BytesRange: input.BytesRange, LineRange: input.LineRange, Mode: input.Mode})
	if e != nil {
		return e
	}
	populateReadOutput(output, &readTarget{fullURI: a.uri}, selection.Text, len(text), selection.Returned, selection.Remaining, selection.StartLine, selection.EndLine, selection.ModeApplied, true, false, selection.OffsetBytes)
	output.Version = a.version
	output.Coverage = &ResourceCoverage{Selection: input.Select, Truncated: selection.Remaining > 0}
	return nil
}

func validTextPrefix(data []byte, partial bool) bool {
	if partial {
		for trim := 0; trim < utf8.UTFMax && len(data) > 0 && !utf8.Valid(data); trim++ {
			data = data[:len(data)-1]
		}
	}
	return utf8.Valid(data) && !isBinaryContent(data)
}

func (a *asset) pdfReader() (*pdf.Reader, error) {
	if a.stream != nil {
		source, ok := a.stream.(io.ReaderAt)
		if !ok {
			return nil, fmt.Errorf("resource does not support PDF access")
		}
		return pdf.NewReader(source, a.size)
	}
	return pdf.NewReader(bytes.NewReader(a.data), int64(len(a.data)))
}
