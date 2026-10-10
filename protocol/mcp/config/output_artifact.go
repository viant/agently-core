package config

import (
	"fmt"
	"strings"
	"unicode"
)

const MaxOutputArtifactBytes int64 = 64 << 20

// OutputArtifact is an explicit client policy for binary MCP output capture.
// Name is an optional authoritative download filename; no name is inferred
// from remote URIs. MaxBytes bounds aggregate decoded bytes for one result.
type OutputArtifact struct {
	Name     string `json:"name,omitempty" yaml:"name,omitempty"`
	MaxBytes int64  `json:"maxBytes,omitempty" yaml:"maxBytes,omitempty"`
}

func (p OutputArtifact) Validate() error {
	if p.MaxBytes < 0 || p.MaxBytes > MaxOutputArtifactBytes {
		return fmt.Errorf("invalid output artifact byte limit")
	}
	if len(p.Name) > 255 || p.Name == "." || p.Name == ".." || strings.ContainsAny(p.Name, `/\\`) || strings.IndexFunc(p.Name, unicode.IsControl) >= 0 {
		return fmt.Errorf("invalid output artifact filename")
	}
	return nil
}

func (p OutputArtifact) ByteLimit() int64 {
	if p.MaxBytes == 0 {
		return MaxOutputArtifactBytes
	}
	return p.MaxBytes
}
