package resources

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

func tableRowBytes(row []string) int { b, _ := json.Marshal(row); return len(b) + 1 }
func selectorDigest(sel *ResourceSelection, options *ResourceOptions) string {
	b, _ := json.Marshal([]interface{}{sel, options})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func encodeReadCursor(version string, sel *ResourceSelection, options *ResourceOptions, offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(version + ":" + selectorDigest(sel, options) + ":" + strconv.Itoa(offset)))
}
func decodeReadCursor(cursor, version string, sel *ResourceSelection, options *ResourceOptions) (int, error) {
	b, e := base64.RawURLEncoding.DecodeString(cursor)
	p := strings.Split(string(b), ":")
	if e != nil || len(p) != 3 || p[0] != version || p[1] != selectorDigest(sel, options) {
		return 0, fmt.Errorf("invalid read cursor")
	}
	return strconv.Atoi(p[2])
}
func tableBounds(sel *ResourceSelection) (int, int, int, int, error) {
	c1, r1, c2, r2 := 1, 1, 16384, maxResourceRows
	if sel != nil && sel.Range != "" {
		parts := strings.Split(sel.Range, ":")
		if len(parts) == 1 {
			parts = append(parts, parts[0])
		}
		if len(parts) != 2 {
			return 0, 0, 0, 0, fmt.Errorf("invalid cell range")
		}
		var err error
		c1, r1, err = excelize.CellNameToCoordinates(parts[0])
		if err != nil {
			return 0, 0, 0, 0, err
		}
		c2, r2, err = excelize.CellNameToCoordinates(parts[1])
		if err != nil {
			return 0, 0, 0, 0, err
		}
		if c2 < c1 || r2 < r1 || r2 > maxResourceRows {
			return 0, 0, 0, 0, fmt.Errorf("invalid or oversized range")
		}
	}
	return c1, r1, c2, r2, nil
}
func (a *asset) table(ctx context.Context, sel *ResourceSelection, options *ResourceOptions) (*ResourceTable, error) {
	return a.tablePage(ctx, sel, options, 0, 0, 0, nil)
}

func (a *asset) tablePage(ctx context.Context, sel *ResourceSelection, options *ResourceOptions, offset, rowLimit, byteLimit int, coverage *ResourceCoverage) (*ResourceTable, error) {
	c1, r1, c2, r2, err := tableBounds(sel)
	if err != nil {
		return nil, err
	}
	values := "displayed"
	if options != nil && options.Values != "" {
		values = options.Values
	}
	if values != "raw" && values != "displayed" {
		return nil, fmt.Errorf("values must be raw or displayed; formulas are not recalculated")
	}
	result := &ResourceTable{Rows: [][]string{}, RowNumbers: []int{}, StartColumn: c1}
	if options != nil {
		if options.HeaderRow < 0 {
			return nil, fmt.Errorf("headerRow must be nonnegative")
		}
		if options.Encoding != "" && options.Encoding != "utf-8" {
			return nil, fmt.Errorf("only utf-8 is supported")
		}
		result.HeaderRow = options.HeaderRow
		if options.OCR != "" {
			return nil, fmt.Errorf("OCR is not a table option")
		}
	}
	var next func() ([]string, error)
	var closeFn func() error
	switch a.kind {
	case "workbook":
		f, e := a.workbook(sel)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		sheet, e := selectSheet(f, sel)
		if e != nil {
			return nil, e
		}
		result.Sheet = sheet
		rows, e := f.Rows(sheet)
		if e != nil {
			return nil, e
		}
		closeFn = rows.Close
		next = func() ([]string, error) {
			if !rows.Next() {
				if e := rows.Error(); e != nil {
					return nil, e
				}
				return nil, io.EOF
			}
			return rows.Columns(excelize.Options{RawCellValue: values == "raw"})
		}
	case "csv":
		if sel != nil && (sel.Sheet != "" || sel.ComponentID != "" || len(sel.Pages) > 0) {
			return nil, fmt.Errorf("CSV has no sheets or pages")
		}
		source, e := a.reader()
		if e != nil {
			return nil, e
		}
		bounded := &csvRecordReader{reader: source}
		reader := csv.NewReader(bounded)
		reader.FieldsPerRecord = -1
		if options != nil {
			if options.Encoding != "" && options.Encoding != "utf-8" {
				return nil, fmt.Errorf("only utf-8 is supported")
			}
			if options.Delimiter != "" {
				r := []rune(options.Delimiter)
				if len(r) != 1 {
					return nil, fmt.Errorf("delimiter must be one character")
				}
				reader.Comma = r[0]
			}
		}
		next = func() ([]string, error) { bounded.remaining = 4 << 20; return reader.Read() }
	default:
		return nil, fmt.Errorf("table extraction unsupported for %s", a.kind)
	}
	if closeFn != nil {
		defer closeFn()
	}
	selectedRows := 0
	cells := 0
	outputBytes := 0
	for rowNum := 1; ; rowNum++ {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if rowNum > r2 && sel != nil && sel.Range != "" {
			break
		}
		row, e := next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if rowNum > maxResourceRows {
			return nil, fmt.Errorf("resource row processing limit exceeded")
		}
		cells += len(row)
		if cells > maxResourceCells {
			return nil, fmt.Errorf("resource cell processing limit exceeded")
		}
		if rowNum < r1 {
			continue
		}
		if rowNum > r2 {
			break
		}
		if selectedRows < offset {
			selectedRows++
			continue
		}
		selectedRows++
		end := c2
		if end > len(row) {
			end = len(row)
		}
		var selected []string
		if c1 <= len(row) {
			selected = append([]string{}, row[c1-1:end]...)
		} else {
			selected = []string{}
		}
		rowBytes := tableRowBytes(selected)
		if rowLimit > 0 && (len(result.Rows) >= rowLimit || outputBytes+rowBytes > byteLimit) {
			if len(result.Rows) == 0 {
				return nil, fmt.Errorf("row exceeds output limit; select fewer columns")
			}
			if coverage != nil {
				coverage.Truncated = true
				coverage.NextRow = rowNum
			}
			break
		}
		outputBytes += rowBytes
		if outputBytes > 64<<20 {
			return nil, fmt.Errorf("table exceeds processing byte limit")
		}
		result.Rows = append(result.Rows, selected)
		result.RowNumbers = append(result.RowNumbers, rowNum)
	}
	if selectedRows < offset {
		return nil, fmt.Errorf("invalid read cursor")
	}
	if coverage != nil {
		coverage.ReturnedRows = len(result.Rows)
	}
	return result, nil
}
func (a *asset) text(ctx context.Context, sel *ResourceSelection, options *ResourceOptions) (string, error) {
	if a.kind == "text" || a.kind == "csv" {
		if sel != nil {
			return "", fmt.Errorf("use byte/line ranges for text")
		}
		if err := a.materialize(); err != nil {
			return "", err
		}
		return string(a.data), nil
	}
	if a.kind != "pdf" {
		return "", fmt.Errorf("text extraction unsupported for %s", a.kind)
	}
	if options != nil && options.OCR != "" && options.OCR != "off" {
		return "", fmt.Errorf("OCR backend unavailable; use native presentation")
	}
	if sel != nil && (sel.Sheet != "" || sel.Range != "" || sel.ComponentID != "") {
		return "", fmt.Errorf("PDF text requires a pages selector")
	}
	r, e := a.pdfReader()
	if e != nil {
		return "", e
	}
	pages := []int{}
	if sel != nil {
		pages = sel.Pages
	}
	if len(pages) == 0 {
		for i := 1; i <= r.NumPage(); i++ {
			pages = append(pages, i)
		}
	}
	if len(pages) > 1000 {
		return "", fmt.Errorf("PDF page limit exceeded; select pages")
	}
	var b strings.Builder
	for _, i := range pages {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		if i < 1 || i > r.NumPage() {
			return "", fmt.Errorf("invalid PDF page %d", i)
		}
		text, e := r.Page(i).GetPlainText(nil)
		if e != nil {
			return "", e
		}
		fmt.Fprintf(&b, "[Page %d]\n%s\n", i, text)
		if b.Len() > 64<<20 {
			return "", fmt.Errorf("PDF text limit exceeded")
		}
	}
	return b.String(), nil
}

// The CSV decoder may otherwise allocate an arbitrarily large single record.
// Its own bounded read-ahead survives between records; reset this processing
// allowance only when starting another record, never for each underlying read.
type csvRecordReader struct {
	reader    io.Reader
	remaining int
}

func (r *csvRecordReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, fmt.Errorf("CSV record exceeds processing byte limit")
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= n
	return n, err
}
