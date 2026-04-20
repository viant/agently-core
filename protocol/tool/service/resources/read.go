package resources

import (
	"context"
	"encoding/base64"
	"fmt"
	pathpkg "path"
	"strings"
	"unicode/utf8"

	"github.com/viant/afs"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/internal/textutil"
	mcpuri "github.com/viant/agently-core/protocol/mcp/uri"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"github.com/viant/agently-core/protocol/tool/service/shared/imageio"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	mcpfs "github.com/viant/agently-core/service/augmenter/mcpfs"
	"github.com/viant/mcp-protocol/extension"
)

type ReadInput struct {
	// RootURI is the normalized or user-provided root URI. Prefer using
	// RootID when possible; RootURI is retained for backward compatibility
	// but hidden from public schemas.
	RootURI string `json:"root,omitempty" internal:"true"`
	// RootID is a stable identifier corresponding to a root returned by
	// roots. When provided (and URI is empty), it is resolved to
	// the underlying normalized URI before enforcement and reading.
	RootID string `json:"rootId,omitempty"`
	Path   string `json:"path,omitempty"`
	URI    string `json:"uri,omitempty"`

	// Range selectors; nested objects accepted by JSON schema
	BytesRange textutil.BytesRange `json:"bytesRange,omitempty"`
	textutil.LineRange

	// MaxBytes caps the returned payload when neither byte nor line ranges are provided.
	// When zero, defaults are applied.
	MaxBytes int `json:"maxBytes,omitempty"`

	// Mode provides lightweight previews without full reads:
	// head (default), tail, signatures.
	Mode string `json:"mode,omitempty"`
}

// ReadOutput contains the resolved URI, relative path and optionally truncated content.
type ReadOutput struct {
	URI       string `json:"uri"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	SkillName string `json:"skillName,omitempty"`
	Size      int    `json:"size"`
	// Returned and Remaining describe how much of the original payload was
	// returned after applying caps/ranges.
	Returned  int `json:"returned,omitempty"`
	Remaining int `json:"remaining,omitempty"`
	// StartLine and EndLine are 1-based line numbers describing the selected
	// slice when Offset/Limit were provided. They are zero when the entire
	// (possibly MaxBytes-truncated) file content is returned.
	StartLine int `json:"startLine,omitempty"`
	EndLine   int `json:"endLine,omitempty"`
	// Binary is true when the content was detected as binary and not fully returned.
	Binary bool `json:"binary,omitempty"`
	// ModeApplied echoes the preview mode applied.
	ModeApplied string `json:"modeApplied,omitempty"`
	// Continuation carries paging/truncation hints when content was clipped.
	Continuation *extension.Continuation `json:"continuation,omitempty"`
}

type ReadImageInput struct {
	// URI is an absolute URI; when provided, RootURI/RootID/Path are ignored.
	URI string `json:"uri,omitempty"`
	// RootURI/RootID + Path select an image under a root.
	RootURI string `json:"root,omitempty"`
	RootID  string `json:"rootId,omitempty"`
	Path    string `json:"path,omitempty"`

	// MaxWidth/MaxHeight define a resize-to-fit box; default 2048x768.
	MaxWidth  int `json:"maxWidth,omitempty"`
	MaxHeight int `json:"maxHeight,omitempty"`
	// MaxBytes caps the encoded output bytes; default 4MB.
	MaxBytes int `json:"maxBytes,omitempty"`

	// Format optionally forces output encoding: "png" or "jpeg".
	Format string `json:"format,omitempty"`

	// IncludeData controls whether dataBase64 is returned in the tool response.
	// When false (default), the tool writes the encoded image to EncodedURI and
	// omits dataBase64 to keep tool output small.
	IncludeData bool `json:"includeData,omitempty"`
	// DestURL optionally specifies where to write the encoded image (file://...).
	DestURL string `json:"destURL,omitempty"`
}

type ReadImageOutput struct {
	URI      string `json:"uri"`
	Encoded  string `json:"encodedURI,omitempty"`
	Path     string `json:"path"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Bytes    int    `json:"bytes"`
	Base64   string `json:"dataBase64,omitempty"`
}

func (s *Service) read(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ReadInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ReadOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	target, err := s.resolveReadTarget(ctx, input, s.agentAllowed(ctx))
	if err != nil {
		logx.Debugf("resources", "read resolve error rootId=%q root=%q uri=%q err=%v", input.RootID, input.RootURI, input.URI, err)
		return err
	}
	if s.skillSvc != nil {
		if convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx)); convID != "" {
			if strings.EqualFold(pathpkg.Base(target.fullURI), "SKILL.md") {
				if body, err := s.skillSvc.ActivateByPathForConversation(ctx, convID, target.fullURI, ""); err == nil {
					output.URI = target.fullURI
					output.Path = strings.TrimSpace(input.Path)
					if output.Path == "" {
						output.Path = target.fullURI
					}
					output.Content = body
					if base := pathpkg.Base(pathpkg.Dir(target.fullURI)); strings.TrimSpace(base) != "" {
						output.SkillName = strings.TrimSpace(base)
					}
					output.Size = len(body)
					output.Returned = len(body)
					return nil
				}
			}
		}
	}
	data, err := s.downloadResource(ctx, target.fullURI)
	if err != nil {
		logx.Debugf("resources", "read download error uri=%q err=%v", target.fullURI, err)
		return err
	}
	selection, err := applyReadSelection(data, input)
	if err != nil {
		logx.Debugf("resources", "read selection error uri=%q err=%v", target.fullURI, err)
		return err
	}
	limitRequested := readLimitRequested(input)
	populateReadOutput(output, target, selection.Text, len(data), selection.Returned, selection.Remaining, selection.StartLine, selection.EndLine, selection.ModeApplied, limitRequested, selection.Binary, selection.OffsetBytes)
	return nil
}

func (s *Service) readImage(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ReadImageInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ReadImageOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	readTarget, err := s.resolveReadTarget(ctx, &ReadInput{
		URI:     input.URI,
		RootURI: input.RootURI,
		RootID:  input.RootID,
		Path:    input.Path,
	}, s.agentAllowed(ctx))
	if err != nil {
		return err
	}
	raw, err := s.downloadResource(ctx, readTarget.fullURI)
	if err != nil {
		return err
	}
	options := imageio.NormalizeOptions(imageio.Options{
		MaxWidth:  input.MaxWidth,
		MaxHeight: input.MaxHeight,
		MaxBytes:  input.MaxBytes,
		Format:    strings.TrimSpace(input.Format),
	})
	encoded, err := imageio.EncodeToFit(raw, options)
	if err != nil {
		return err
	}
	output.URI = readTarget.fullURI
	output.Path = strings.TrimSpace(input.Path)
	if output.Path == "" {
		output.Path = strings.TrimSpace(readTarget.fullURI)
	}
	output.Name = pathpkg.Base(output.Path)
	output.MimeType = encoded.MimeType
	output.Width = encoded.Width
	output.Height = encoded.Height
	output.Bytes = len(encoded.Bytes)
	encodedURI, err := imageio.StoreEncodedImage(ctx, encoded, imageio.StoreOptions{DestURL: strings.TrimSpace(input.DestURL)})
	if err != nil {
		return err
	}
	output.Encoded = encodedURI
	if input.IncludeData {
		output.Base64 = base64.StdEncoding.EncodeToString(encoded.Bytes)
	}
	return nil
}

type readTarget struct {
	fullURI  string
	normRoot string
}

func (s *Service) resolveReadTarget(ctx context.Context, input *ReadInput, allowed []string) (*readTarget, error) {
	uri := strings.TrimSpace(input.URI)
	if uri != "" {
		fullURI, err := s.normalizeFullURI(ctx, uri, allowed)
		if err != nil {
			return nil, err
		}
		return &readTarget{fullURI: fullURI}, nil
	}
	rootCtx, err := s.newRootContext(ctx, input.RootURI, input.RootID, allowed)
	if err != nil {
		return nil, err
	}
	pathPart := strings.TrimSpace(input.Path)
	if pathPart == "" {
		return nil, fmt.Errorf("path is required when uri is not provided")
	}
	fullURI, err := rootCtx.ResolvePath(pathPart)
	if err != nil {
		return nil, err
	}
	return &readTarget{fullURI: fullURI, normRoot: rootCtx.Base()}, nil
}

func readLimitRequested(input *ReadInput) bool {
	if input == nil {
		return false
	}
	if strings.TrimSpace(input.Mode) != "" {
		return true
	}
	if input.MaxBytes > 0 || input.LineCount > 0 {
		return true
	}
	if input.BytesRange.OffsetBytes > 0 || input.BytesRange.LengthBytes > 0 {
		return true
	}
	if input.StartLine > 0 {
		return true
	}
	return false
}

func (s *Service) downloadResource(ctx context.Context, uri string) ([]byte, error) {
	if mcpuri.Is(uri) {
		mfs, err := s.mcpFS(ctx)
		if err != nil {
			return nil, fmt.Errorf("resources: download mcp uri=%q: %w", uri, err)
		}
		data, err := mfs.DownloadDirect(ctx, mcpfs.NewObjectFromURI(uri))
		if err == nil {
			return data, nil
		}
		logx.Debugf("resources", "download direct failed uri=%q err=%v; falling back to snapshot", uri, err)
		return mfs.Download(ctx, mcpfs.NewObjectFromURI(uri))
	}
	fs := afs.New()
	return fs.DownloadWithURL(ctx, uri)
}

type readSelection struct {
	Text        string
	StartLine   int
	EndLine     int
	ModeApplied string
	Returned    int
	Remaining   int
	Binary      bool
	// OffsetBytes records the starting byte offset used for this selection (0 when head/tail without explicit range).
	OffsetBytes int
}

func applyReadSelection(data []byte, input *ReadInput) (*readSelection, error) {
	const defaultMaxBytes = 8192
	appliedMode := strings.TrimSpace(strings.ToLower(input.Mode))
	if appliedMode == "" {
		appliedMode = "head"
	}
	startLine := 0
	endLine := 0

	// Binary guard: avoid returning raw binary payloads.
	if isBinaryContent(data) {
		return &readSelection{
			Text:        "[binary content omitted]",
			StartLine:   0,
			EndLine:     0,
			ModeApplied: appliedMode,
			Returned:    0,
			Remaining:   len(data),
			Binary:      true,
			OffsetBytes: 0,
		}, nil
	}

	// Compute selection into these variables, then return once at the end.
	var text string
	var returned, remaining, offsetBytes int

	// Default text is the whole file; branches below will override.
	text = string(data)

	// Byte range selection
	if input.BytesRange.OffsetBytes > 0 || input.BytesRange.LengthBytes > 0 {
		clipped, start, _, err := textutil.ClipBytesByRange(data, input.BytesRange)
		if err != nil {
			return nil, err
		}
		text = string(clipped)
		offsetBytes = start
		returned = len(text)
		remaining = len(data) - (start + returned)
		if remaining < 0 {
			remaining = 0
		}
	} else if input.StartLine > 0 {
		// Line range selection
		lineRange := textutil.LineRange{StartLine: input.StartLine, LineCount: input.LineCount}
		if lineRange.LineCount < 0 {
			lineRange.LineCount = 0
		}
		clipped, start, _, err := textutil.ClipLinesByRange(data, lineRange)
		if err != nil {
			return nil, err
		}
		text = string(clipped)
		startLine = input.StartLine
		if lineRange.LineCount > 0 {
			endLine = startLine + lineRange.LineCount - 1
		}
		offsetBytes = start
		returned = len(text)
		remaining = len(data) - (start + returned)
		if remaining < 0 {
			remaining = 0
		}
	} else {
		// Head/Tail/Signatures modes
		maxBytes := input.MaxBytes
		if maxBytes <= 0 {
			maxBytes = defaultMaxBytes
		}
		maxLines := input.LineCount
		if maxLines < 0 {
			maxLines = 0
		}
		text, returned, remaining = applyMode(text, len(data), appliedMode, maxBytes, maxLines)
		offsetBytes = 0
	}

	rs := &readSelection{
		Text:        text,
		StartLine:   startLine,
		EndLine:     endLine,
		ModeApplied: appliedMode,
		Returned:    returned,
		Remaining:   remaining,
		Binary:      false,
		OffsetBytes: offsetBytes,
	}
	return rs, nil
}

func applyMode(text string, totalSize int, mode string, maxBytes, maxLines int) (string, int, int) {
	switch mode {
	case "tail":
		return textutil.ClipTail(text, totalSize, maxBytes, maxLines)
	case "signatures":
		if sig := textutil.ExtractSignatures(text, maxBytes); sig != "" {
			return sig, len(sig), clipRemaining(totalSize, len(sig))
		}
		// fallback to head if no signatures found
	}
	return textutil.ClipHead(text, totalSize, maxBytes, maxLines)
}

func clipRemaining(totalSize, returned int) int {
	if totalSize <= returned {
		return 0
	}
	return totalSize - returned
}

func isBinaryContent(data []byte) bool {
	if !utf8.Valid(data) {
		return true
	}
	const maxInspect = 1024
	limit := len(data)
	if limit > maxInspect {
		limit = maxInspect
	}
	control := 0
	for _, b := range data[:limit] {
		if b == 0 {
			return true
		}
		if b < 32 && b != '\n' && b != '\r' && b != '\t' {
			control++
		}
	}
	return control > limit/10
}

func populateReadOutput(out *ReadOutput, target *readTarget, content string, size, returned, remaining, startLine, endLine int, mode string, limitRequested bool, binary bool, byteOffset int) {
	out.URI = target.fullURI
	if target.normRoot != "" {
		out.Path = relativePath(target.normRoot, target.fullURI)
	} else {
		out.Path = target.fullURI
	}
	out.Content = content
	out.Size = size
	out.Returned = returned
	out.Remaining = remaining
	out.StartLine = startLine
	out.EndLine = endLine
	out.ModeApplied = mode
	out.Binary = binary
	truncated := returned > 0 && size > returned
	if !limitRequested {
		truncated = false
	}
	if remaining <= 0 && truncated {
		remaining = size - (byteOffset + returned)
		if remaining < 0 {
			remaining = 0
		}
		out.Remaining = remaining
	}
	if limitRequested && (remaining > 0 || truncated) {
		out.Continuation = &extension.Continuation{
			HasMore:   true,
			Remaining: remaining,
			Returned:  returned,
			Mode:      mode,
			Binary:    binary,
		}
		if out.Continuation.Remaining < 0 {
			out.Continuation.Remaining = 0
		}
		if out.Continuation.Returned < 0 {
			out.Continuation.Returned = 0
		}
		// Compute next byte range based on current offset and returned size.
		nextOffset := byteOffset + returned
		nextLength := returned
		if remaining > 0 && nextLength > remaining {
			nextLength = remaining
		}
		if remaining <= 0 {
			// No continuation when nothing remains.
			out.Continuation = nil
		} else {
			out.Continuation.NextRange = &extension.RangeHint{
				Bytes: &extension.ByteRange{
					Offset: nextOffset,
					Length: nextLength,
				},
			}
			// Optionally include line hints when present
			if endLine > 0 && startLine > 0 {
				count := endLine - startLine + 1
				if count < 0 {
					count = 0
				}
				out.Continuation.NextRange.Lines = &extension.LineRange{Start: endLine + 1, Count: count}
			}
		}
	}
}

func commonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, v := range values[1:] {
		for !strings.HasPrefix(v, prefix) && prefix != "" {
			prefix = prefix[:len(prefix)-1]
		}
		if prefix == "" {
			break
		}
	}
	// Avoid cutting in the middle of a path segment when possible.
	if i := strings.LastIndex(prefix, "/"); i > 0 {
		return prefix[:i+1]
	}
	return prefix
}
