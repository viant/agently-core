package resources

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
)

const (
	tableauKind               = "tableau"
	maxTableauDefinitionBytes = 64 << 20
	maxTableauDirectoryBytes  = 8 << 20
	maxTableauEntries         = 10000
)

func tableauExtension(name string) string {
	switch ext := strings.ToLower(path.Ext(name)); ext {
	case ".twb", ".twbx":
		return ext
	default:
		return ""
	}
}

type tableauMetadata struct {
	properties map[string]string
	components []ResourceComponent
}

var errTableauDirectoryLimit = errors.New("Tableau archive directory exceeds inspection limit")

type tableauArchiveReader struct {
	ctx       context.Context
	source    io.ReaderAt
	bytesLeft int
	indexOnly bool
}

func (r *tableauArchiveReader) ReadAt(data []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.indexOnly && len(data) > r.bytesLeft {
		return 0, errTableauDirectoryLimit
	}
	n, err := r.source.ReadAt(data, offset)
	if r.indexOnly {
		r.bytesLeft -= n
	}
	return n, err
}

func (a *asset) tableauMetadata(ctx context.Context) (*tableauMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tableauExtension(a.name) == ".twb" {
		if a.size > maxTableauDefinitionBytes {
			return nil, fmt.Errorf("Tableau definition exceeds inspection limit")
		}
		source, err := a.reader()
		if err != nil {
			return nil, err
		}
		return parseTableauMetadata(ctx, source, path.Base(a.name), false)
	}
	sourceAt, err := a.archiveReaderAt()
	if err != nil {
		return nil, err
	}
	indexReader := &tableauArchiveReader{ctx: ctx, source: sourceAt, bytesLeft: maxTableauDirectoryBytes, indexOnly: true}
	archive, err := zip.NewReader(indexReader, a.size)
	if err != nil {
		if errors.Is(err, errTableauDirectoryLimit) || ctx.Err() != nil {
			return nil, err
		}
		return nil, fmt.Errorf("invalid Tableau archive")
	}
	indexReader.indexOnly = false
	if len(archive.File) > maxTableauEntries {
		return nil, fmt.Errorf("Tableau archive entry count exceeds inspection limit")
	}
	names := make(map[string]bool, len(archive.File))
	var definition *zip.File
	hasExtract := false
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(entry.Name, "/")
		key := strings.ToLower(name)
		if name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, "\\:\x00") || names[key] || entry.Mode().Type()&^fs.ModeDir != 0 {
			return nil, fmt.Errorf("invalid or conflicting Tableau archive entry")
		}
		names[key] = true
		if entry.FileInfo().IsDir() {
			continue
		}
		switch strings.ToLower(path.Ext(name)) {
		case ".twb":
			if definition != nil {
				return nil, fmt.Errorf("Tableau archive contains multiple definitions")
			}
			definition = entry
		case ".hyper", ".tde":
			hasExtract = true
		}
	}
	if definition == nil {
		return nil, fmt.Errorf("Tableau archive contains no workbook definition")
	}
	if definition.UncompressedSize64 > maxTableauDefinitionBytes {
		return nil, fmt.Errorf("Tableau definition exceeds inspection limit")
	}
	source, err := definition.Open()
	if err != nil {
		return nil, fmt.Errorf("cannot open Tableau definition")
	}
	defer source.Close()
	return parseTableauMetadata(ctx, source, path.Base(definition.Name), hasExtract)
}

func (m *tableauMetadata) selectComponents(selection *ResourceSelection) ([]ResourceComponent, error) {
	if selection == nil {
		return m.components, nil
	}
	if selection.ComponentID == "" || selection.Sheet != "" || selection.Range != "" || len(selection.Pages) != 0 {
		return nil, fmt.Errorf("Tableau metadata selection requires a componentId")
	}
	selected := map[string]bool{selection.ComponentID: true}
	var result []ResourceComponent
	for _, component := range m.components {
		if selected[component.ID] || selected[component.ParentID] {
			selected[component.ID] = true
			result = append(result, component)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("unknown Tableau component")
	}
	return result, nil
}

func tableauPage(components []ResourceComponent, start, limit, maxBytes int) ([]ResourceComponent, int, error) {
	if start < 0 || start > len(components) {
		return nil, 0, fmt.Errorf("invalid Tableau metadata cursor")
	}
	end := start
	size := 2 // JSON array delimiters.
	for end < len(components) && end-start < limit {
		raw, err := json.Marshal(components[end])
		if err != nil {
			return nil, 0, err
		}
		if size+len(raw)+1 > maxBytes {
			if start == end {
				return nil, 0, fmt.Errorf("Tableau component exceeds output byte limit")
			}
			break
		}
		size += len(raw) + 1
		end++
	}
	return components[start:end], size, nil
}

func (a *asset) readTableauMetadata(ctx context.Context, input *ReadInput, output *ReadOutput, maxRows, maxBytes int) error {
	if input.Options != nil || input.BytesRange.OffsetBytes != 0 || input.BytesRange.LengthBytes != 0 ||
		input.StartLine != 0 || input.LineCount != 0 || input.Mode != "" {
		return fmt.Errorf("Tableau metadata uses component selection and pagination")
	}
	meta, err := a.tableauMetadata(ctx)
	if err != nil {
		return err
	}
	components, err := meta.selectComponents(input.Select)
	if err != nil {
		return err
	}
	start := 0
	if input.Cursor != "" {
		start, err = decodeReadCursor(input.Cursor, a.version, input.Select, nil)
		if err != nil {
			return err
		}
	}
	page, size, err := tableauPage(components, start, maxRows, maxBytes)
	if err != nil {
		return err
	}
	next := start + len(page)
	*output = ReadOutput{
		URI: a.uri, Size: int(a.size), Version: a.version,
		Metadata: meta.properties, Components: page, Returned: size,
		Coverage: &ResourceCoverage{Selection: input.Select, Truncated: next < len(components)},
	}
	if output.Coverage.Truncated {
		output.Cursor = encodeReadCursor(a.version, input.Select, nil, next)
	}
	return ctx.Err()
}
