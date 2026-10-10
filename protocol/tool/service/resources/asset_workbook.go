package resources

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"

	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	"github.com/xuri/excelize/v2"
)

type workbookSheet struct{ name, target, dimension string }

func (a *asset) archive() (*zip.Reader, error) {
	if a.stream != nil {
		reader, ok := a.stream.(io.ReaderAt)
		if !ok {
			return nil, fmt.Errorf("resource does not support workbook access")
		}
		return zip.NewReader(reader, a.size)
	}
	return zip.NewReader(bytes.NewReader(a.data), int64(len(a.data)))
}

// Workbook inspection reads the directory and small XML metadata from the
// verified spool. Worksheet cells and embedded media are never materialized.
func (a *asset) workbookMetadata() ([]workbookSheet, error) {
	archive, err := a.archive()
	if err != nil {
		return nil, err
	}
	if len(archive.File) > 100000 {
		return nil, fmt.Errorf("workbook entry count exceeds inspection limit")
	}
	files := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		files[file.Name] = file
	}
	readMetadata := func(name string, value interface{}) error {
		file := files[name]
		if file == nil {
			return fmt.Errorf("invalid workbook metadata")
		}
		if file.UncompressedSize64 > 1<<20 {
			return fmt.Errorf("workbook metadata exceeds inspection limit")
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		defer reader.Close()
		return xml.NewDecoder(io.LimitReader(reader, (1<<20)+1)).Decode(value)
	}
	var book struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err = readMetadata("xl/workbook.xml", &book); err != nil {
		return nil, err
	}
	if len(book.Sheets) == 0 || len(book.Sheets) > 1000 {
		return nil, fmt.Errorf("workbook sheet count exceeds inspection limit")
	}
	var rels struct {
		Items []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
			Mode   string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if err = readMetadata("xl/_rels/workbook.xml.rels", &rels); err != nil {
		return nil, err
	}
	targets := map[string]string{}
	for _, rel := range rels.Items {
		if rel.Mode == "External" {
			continue
		}
		target := strings.TrimPrefix(rel.Target, "/")
		if !strings.HasPrefix(rel.Target, "/") {
			target = path.Join("xl", rel.Target)
		}
		if !strings.HasPrefix(target, "xl/") {
			return nil, fmt.Errorf("invalid workbook relationship")
		}
		targets[rel.ID] = target
	}
	result := make([]workbookSheet, 0, len(book.Sheets))
	for _, sheet := range book.Sheets {
		target := targets[sheet.ID]
		file := files[target]
		if file == nil {
			return nil, fmt.Errorf("invalid workbook sheet relationship")
		}
		entry := workbookSheet{name: sheet.Name, target: target}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		decoder := xml.NewDecoder(io.LimitReader(reader, 1<<20))
		for {
			token, err := decoder.Token()
			if err != nil {
				break
			}
			start, ok := token.(xml.StartElement)
			if !ok {
				continue
			}
			if start.Name.Local == "dimension" {
				for _, attr := range start.Attr {
					if attr.Name.Local == "ref" {
						entry.dimension = attr.Value
					}
				}
				break
			}
			if start.Name.Local == "sheetData" {
				break
			}
		}
		_ = reader.Close()
		result = append(result, entry)
	}
	return result, nil
}

// Excelize reads its entire ZIP input into memory. Repackage only the selected
// sheet and value/style dependencies with the existing extraction budget; large
// unrelated sheets, drawings, or media therefore do not enter that allocation.
func (a *asset) selectedWorkbook(selection *ResourceSelection) (*excelize.File, error) {
	sheets, err := a.workbookMetadata()
	if err != nil {
		return nil, err
	}
	chosen := ""
	if selection != nil {
		chosen = selection.Sheet
		if selection.ComponentID != "" {
			for i, sheet := range sheets {
				if selection.ComponentID == fmt.Sprintf("sheet-%d", i+1) {
					if chosen != "" && chosen != sheet.name {
						return nil, fmt.Errorf("conflicting sheet selectors")
					}
					chosen = sheet.name
				}
			}
			if chosen == "" {
				return nil, fmt.Errorf("unknown sheet component")
			}
		}
	}
	if chosen == "" {
		if len(sheets) != 1 {
			return nil, fmt.Errorf("select a sheet; inspect the workbook first")
		}
		chosen = sheets[0].name
	}
	target := ""
	for _, sheet := range sheets {
		if sheet.name == chosen {
			target = sheet.target
		}
	}
	if target == "" {
		return nil, fmt.Errorf("unknown sheet %q", chosen)
	}
	archive, err := a.archive()
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	total := uint64(0)
	for _, file := range archive.File {
		switch file.Name {
		case "[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/sharedStrings.xml", target:
		default:
			continue
		}
		if file.UncompressedSize64 > uint64(scratchpadsvc.MaxArtifactBytes)-total {
			_ = writer.Close()
			return nil, fmt.Errorf("selected workbook exceeds processing byte limit")
		}
		total += file.UncompressedSize64
		source, err := file.Open()
		if err != nil {
			_ = writer.Close()
			return nil, err
		}
		header := file.FileHeader
		header.Method = zip.Deflate
		dest, err := writer.CreateHeader(&header)
		if err == nil {
			var count int64
			count, err = io.Copy(dest, io.LimitReader(source, int64(file.UncompressedSize64)+1))
			if err == nil && uint64(count) != file.UncompressedSize64 {
				err = fmt.Errorf("invalid workbook entry size")
			}
		}
		_ = source.Close()
		if err != nil {
			_ = writer.Close()
			return nil, err
		}
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	return excelize.OpenReader(bytes.NewReader(buffer.Bytes()), excelize.Options{UnzipSizeLimit: scratchpadsvc.MaxArtifactBytes, UnzipXMLSizeLimit: 8 << 20})
}
