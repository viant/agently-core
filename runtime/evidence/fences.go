package evidence

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// FenceTransform validates and consumes a complete authored structured fence.
// Returning an error prevents any bytes of that fence from being published.
type FenceTransform func(kind, body string) (string, error)

// FenceStream buffers structured fences across arbitrary provider byte chunks.
// Ordinary complete lines can progress while a later fence is being validated.
// The caller owns one instance per message and serializes calls. On cancellation
// discard the instance: buffered, unvalidated content is never released.
type FenceStream struct {
	transform FenceTransform
	pending   string
	kind      string
	body      strings.Builder
	failed    error
	ended     bool
}

func NewFenceStream(transform FenceTransform) *FenceStream { return &FenceStream{transform: transform} }

func (s *FenceStream) Push(delta string, final bool) (string, error) {
	if s.failed != nil {
		return "", s.failed
	}
	if s.ended {
		return "", fmt.Errorf("evidence stream already finalized")
	}
	s.pending += delta
	var output strings.Builder
	for {
		end := strings.IndexByte(s.pending, '\n')
		if end < 0 && !final {
			break
		}
		if end < 0 && s.pending == "" {
			break
		}
		line := s.pending
		if end >= 0 {
			line = s.pending[:end+1]
			s.pending = s.pending[end+1:]
		} else {
			s.pending = ""
		}
		if !utf8.ValidString(line) {
			s.failed = fmt.Errorf("evidence stream invalid UTF-8")
			return output.String(), s.failed
		}
		stripped := strings.TrimSpace(line)
		if s.kind == "" {
			kind := strings.TrimSpace(strings.TrimPrefix(stripped, "```"))
			if strings.HasPrefix(stripped, "```") && (kind == "forge-data" || kind == "forge-report" || kind == "forge-config") {
				s.kind = kind
				s.body.Reset()
				continue
			}
			output.WriteString(line)
			continue
		}
		if stripped != "```" {
			s.body.WriteString(line)
			continue
		}
		if s.transform == nil {
			s.failed = fmt.Errorf("evidence fence validator unavailable")
			return output.String(), s.failed
		}
		transformed, err := s.transform(s.kind, strings.TrimSpace(s.body.String()))
		if err != nil {
			s.failed = err
			return output.String(), err
		}
		output.WriteString("```" + s.kind + "\n" + transformed + "\n```")
		if strings.HasSuffix(line, "\n") {
			output.WriteByte('\n')
		}
		s.kind = ""
		s.body.Reset()
	}
	if final {
		s.ended = true
		if s.kind != "" {
			s.failed = fmt.Errorf("evidence %s fence is not closed", s.kind)
			return output.String(), s.failed
		}
	}
	return output.String(), nil
}
