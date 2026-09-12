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
}

func (s *Service) loadAsset(ctx context.Context, in *ReadInput, expected string) (*asset, error) {
	target, err := s.resolveReadTarget(ctx, in, s.agentAllowed(ctx))
	if err != nil {
		return nil, err
	}
	a := &asset{uri: target.fullURI, name: path.Base(target.fullURI)}
	if strings.HasPrefix(a.uri, "scratchpad://") {
		d, e := scratchpadsvc.New().DescribeArtifact(ctx, a.uri)
		if e != nil {
			return nil, e
		}
		a.name = d.Name
		a.mime = d.MimeType
	}
	a.data, err = s.downloadResource(ctx, a.uri)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(a.data)
	a.version = hex.EncodeToString(hash[:])
	if expected != "" && expected != a.version {
		return nil, fmt.Errorf("resource_changed")
	}
	a.kind = "binary"
	switch {
	case bytes.HasPrefix(a.data, []byte("%PDF-")):
		a.kind = "pdf"
		a.mime = "application/pdf"
	case bytes.HasPrefix(a.data, []byte("PK")):
		f, e := excelize.OpenReader(bytes.NewReader(a.data), excelize.Options{UnzipSizeLimit: scratchpadsvc.MaxArtifactBytes, UnzipXMLSizeLimit: 8 << 20})
		if e == nil {
			f.Close()
			a.kind = "workbook"
			a.mime = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		}
	default:
		if _, _, e := image.DecodeConfig(bytes.NewReader(a.data)); e == nil {
			a.kind = "image"
			a.mime = http.DetectContentType(a.data)
		} else if utf8.Valid(a.data) && !isBinaryContent(a.data) {
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
func (a *asset) workbook() (*excelize.File, error) {
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
	*result = InspectOutput{URI: a.uri, Name: a.name, MimeType: a.mime, Kind: a.kind, SizeBytes: len(a.data), Version: a.version, Capabilities: map[string][]string{}, Complete: true, NativeRequiresProviderSupport: true}
	switch a.kind {
	case "workbook":
		f, e := a.workbook()
		if e != nil {
			return e
		}
		defer f.Close()
		result.Capabilities["read"] = []string{"table", "native"}
		result.Capabilities["export"] = []string{"csv", "json", "xlsx"}
		sheets := f.GetSheetList()
		if len(sheets) > 1000 {
			return fmt.Errorf("workbook sheet count exceeds inspection limit")
		}
		for i, n := range sheets {
			dimension, _ := f.GetSheetDimension(n)
			result.Components = append(result.Components, ResourceComponent{ID: fmt.Sprintf("sheet-%d", i+1), Name: n, Kind: "sheet", UsedRange: dimension, UsedRangeEstimated: true})
		}
		if req.Select != nil {
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
		r, e := pdf.NewReader(bytes.NewReader(a.data), int64(len(a.data)))
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
		c, _, e := image.DecodeConfig(bytes.NewReader(a.data))
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
	if a.kind == "workbook" {
		output.Warnings = []string{"Formula values use workbook caches; formulas are not recalculated."}
	}
	output.URI = a.uri
	output.Size = len(a.data)
	output.Version = a.version
	rep := input.Representation
	if rep == "native" {
		if input.Select != nil || input.Options != nil || input.Cursor != "" {
			return fmt.Errorf("native read requires the whole resource; export a selection first")
		}
		snapshotHash := sha256.Sum256([]byte(a.uri + "\x00" + a.version))
		d, e := scratchpadsvc.New().PublishArtifact(ctx, "native-"+hex.EncodeToString(snapshotHash[:]), a.name, a.mime, a.uri, bytes.NewReader(a.data))
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
		table, e := a.table(ctx, input.Select, input.Options)
		if e != nil {
			return e
		}
		start := 0
		if input.Cursor != "" {
			start, e = decodeReadCursor(input.Cursor, a.version, input.Select, input.Options)
			if e != nil {
				return e
			}
		}
		if start < 0 || start > len(table.Rows) {
			return fmt.Errorf("invalid read cursor")
		}
		end := start
		used := 0
		for end < len(table.Rows) && end-start < maxRows {
			n := tableRowBytes(table.Rows[end])
			if used+n > maxBytes {
				break
			}
			used += n
			end++
		}
		if end == start && start < len(table.Rows) {
			return fmt.Errorf("row exceeds output limit; select fewer columns")
		}
		output.Coverage = &ResourceCoverage{Selection: input.Select, ReturnedRows: end - start, Truncated: end < len(table.Rows)}
		if end < len(table.Rows) {
			output.Cursor = encodeReadCursor(a.version, input.Select, input.Options, end)
			output.Coverage.NextRow = table.RowNumbers[end]
		}
		table.Rows = table.Rows[start:end]
		table.RowNumbers = table.RowNumbers[start:end]
		output.Table = table
		output.Returned = used
		return nil
	}
	if rep != "text" {
		return fmt.Errorf("unsupported representation %q", rep)
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
