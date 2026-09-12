package resources

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	"github.com/viant/agently-core/protocol/tool/service/shared/imageio"
	"github.com/xuri/excelize/v2"
)

func (s *Service) exportAsset(ctx context.Context, in, out interface{}) error {
	req, ok := in.(*ExportInput)
	if !ok {
		return fmt.Errorf("invalid export input")
	}
	result, ok := out.(*ExportOutput)
	if !ok {
		return fmt.Errorf("invalid export output")
	}
	a, err := s.loadAsset(ctx, &ReadInput{URI: req.URI, Path: req.Path, RootID: req.RootID}, req.ExpectedVersion)
	if err != nil {
		return err
	}
	format := strings.ToLower(req.Output.Format)
	if req.Operation == "render" || req.Operation == "extractImages" {
		return s.exportPDFMedia(ctx, a, req, result)
	}
	if req.Operation != "convert" {
		return fmt.Errorf("unsupported export operation")
	}
	var data []byte
	mime := ""
	switch format {
	case "csv", "json", "xlsx":
		table, e := a.table(ctx, req.Select, req.Options)
		if e != nil {
			return e
		}
		switch format {
		case "csv":
			var b bytes.Buffer
			w := csv.NewWriter(&b)
			if req.Options != nil && req.Options.Delimiter != "" {
				r := []rune(req.Options.Delimiter)
				if len(r) != 1 {
					return fmt.Errorf("delimiter must be one character")
				}
				w.Comma = r[0]
			}
			if e = w.WriteAll(table.Rows); e != nil {
				return e
			}
			data = b.Bytes()
			mime = "text/csv"
		case "json":
			data, e = json.Marshal(table)
			if e != nil {
				return e
			}
			mime = "application/json"
		case "xlsx":
			f := excelize.NewFile()
			defer f.Close()
			for i, row := range table.Rows {
				if e = ctx.Err(); e != nil {
					return e
				}
				cell, _ := excelize.CoordinatesToCellName(1, i+1)
				if e = f.SetSheetRow("Sheet1", cell, &row); e != nil {
					return e
				}
			}
			b, e := f.WriteToBuffer()
			if e != nil {
				return e
			}
			data = b.Bytes()
			mime = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		}
	case "txt":
		text, e := a.text(ctx, req.Select, req.Options)
		if e != nil {
			return e
		}
		data = []byte(text)
		mime = "text/plain"
	case "png", "jpeg":
		if a.kind != "image" || req.Select != nil {
			return fmt.Errorf("image conversion requires a whole image")
		}
		if err = validateImageInput(a.data); err != nil {
			return err
		}
		image, e := imageio.EncodeToFit(a.data, imageio.NormalizeOptions(imageio.Options{Format: format}))
		if e != nil {
			return e
		}
		data = image.Bytes
		mime = image.MimeType
	default:
		return fmt.Errorf("unsupported export format %q", format)
	}
	d, err := scratchpadsvc.New().PublishArtifact(ctx, "", strings.TrimSuffix(a.name, path.Ext(a.name))+"."+format, mime, a.uri, bytes.NewReader(data))
	if err != nil {
		return err
	}
	*result = ExportOutput{Resources: []*scratchpadsvc.ArtifactDescriptor{d}, SourceVersion: a.version, Complete: true}
	if a.kind == "workbook" {
		result.Warnings = []string{"Exports contain selected values; formulas, formatting, and external references are not preserved."}
	}
	return nil
}
