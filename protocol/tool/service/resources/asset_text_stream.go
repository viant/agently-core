package resources

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// readStreamText only holds the requested bounded preview, even when the
// verified artifact is much larger than the legacy input-upload limit.
func (a *asset) readStreamText(ctx context.Context, input *ReadInput, output *ReadOutput, limit int) error {
	if limit <= 0 {
		limit = 8192
	}
	if limit > maxReadOutputBytes {
		limit = maxReadOutputBytes
	}
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = "head"
	}
	size := int(a.size)
	if a.kind == "binary" || a.kind == "image" || a.kind == "workbook" || a.kind == "pdf" {
		populateReadOutput(output, &readTarget{fullURI: a.uri}, "[binary content omitted]", size, 0, size, 0, 0, mode, true, true, 0)
		output.Version = a.version
		return ctx.Err()
	}
	source, err := a.reader()
	if err != nil {
		return err
	}
	offset, startLine, endLine := int64(0), 0, 0
	if input.BytesRange.OffsetBytes < 0 || input.BytesRange.LengthBytes < 0 {
		return fmt.Errorf("invalid byte range")
	}
	if input.BytesRange.OffsetBytes > 0 || input.BytesRange.LengthBytes > 0 {
		offset = int64(input.BytesRange.OffsetBytes)
		if offset > a.size {
			offset = a.size
		}
		if _, err = a.stream.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		if input.BytesRange.LengthBytes > 0 && input.BytesRange.LengthBytes < limit {
			limit = input.BytesRange.LengthBytes
		}
	} else if input.StartLine > 0 {
		reader := bufio.NewReader(source)
		for line := 1; line < input.StartLine; {
			if err = ctx.Err(); err != nil {
				return err
			}
			fragment, e := reader.ReadSlice('\n')
			offset += int64(len(fragment))
			if e == nil {
				line++
			} else if e != bufio.ErrBufferFull {
				if e != io.EOF {
					return e
				}
				break
			}
		}
		source = reader
		startLine = input.StartLine
	} else if mode == "tail" {
		offset = a.size - int64(limit)
		if offset < 0 {
			offset = 0
		}
		if _, err = a.stream.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	data, err := io.ReadAll(io.LimitReader(source, int64(limit)))
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Clip only boundary fragments, preserving valid UTF-8 output.
	if offset > 0 {
		for len(data) > 0 && !utf8.RuneStart(data[0]) {
			data = data[1:]
			offset++
		}
	}
	for trim := 0; trim < utf8.UTFMax && len(data) > 0 && !utf8.Valid(data); trim++ {
		data = data[:len(data)-1]
	}
	if isBinaryContent(data) {
		populateReadOutput(output, &readTarget{fullURI: a.uri}, "[binary content omitted]", size, 0, size, 0, 0, mode, true, true, int(offset))
		output.Version = a.version
		return nil
	}
	text := string(data)
	if startLine > 0 {
		if input.LineCount > 0 {
			at := 0
			for count := 0; count < input.LineCount; count++ {
				next := strings.IndexByte(text[at:], '\n')
				if next < 0 {
					break
				}
				at += next + 1
				if count+1 == input.LineCount {
					text = text[:at]
				}
			}
		}
		endLine = startLine + strings.Count(text, "\n")
		if strings.HasSuffix(text, "\n") {
			endLine--
		}
	} else if input.BytesRange.OffsetBytes == 0 && input.BytesRange.LengthBytes == 0 {
		text, _, _ = applyMode(text, len(text), mode, limit, input.LineCount)
	}
	remaining := size - int(offset) - len(text)
	if remaining < 0 {
		remaining = 0
	}
	populateReadOutput(output, &readTarget{fullURI: a.uri}, text, size, len(text), remaining, startLine, endLine, mode, true, false, int(offset))
	output.Version = a.version
	output.Coverage = &ResourceCoverage{Truncated: size > len(text)}
	return nil
}
