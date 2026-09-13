// Package tablepreference validates presentation preferences exchanged with an
// external adapter. It does not implement storage or host an MCP server.
package tablepreference

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

const Version = 1
const MaxBytes = 64 << 10

// Schema describes the portable preference document. Validate additionally
// enforces unique column IDs, key hygiene and the encoded payload size limit.
//
//go:embed schema.json
var Schema []byte

// Column order in this slice is the user's preferred display order.
type Column struct {
	ID          string   `json:"id"`
	Visible     *bool    `json:"visible,omitempty"`
	Width       *float64 `json:"width,omitempty"`
	DisplayName *string  `json:"displayName,omitempty"`
	Align       string   `json:"align,omitempty"`
	Tooltip     *string  `json:"tooltip,omitempty"`
}

type Sort struct {
	ColumnID  string `json:"columnId"`
	Direction string `json:"direction"`
}

type Preferences struct {
	Version         int      `json:"version"`
	Columns         []Column `json:"columns,omitempty"`
	Sort            *Sort    `json:"sort,omitempty"`
	Density         string   `json:"density,omitempty"`
	FrozenColumnIDs []string `json:"frozenColumnIds,omitempty"`
}

// ValidateKey treats the key as opaque. The host must namespace it by workspace
// and authenticated principal; it must not trust a caller-supplied user ID.
func ValidateKey(key string) error {
	return identifier("key", key, 512)
}

func identifier(field, value string, max int) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > max {
		return fmt.Errorf("table preferences: invalid %s", field)
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return fmt.Errorf("table preferences: invalid %s", field)
		}
	}
	return nil
}

func Validate(p *Preferences) error {
	if p == nil || p.Version != Version {
		return fmt.Errorf("table preferences: unsupported or missing version")
	}
	if len(p.Columns) > 256 || len(p.FrozenColumnIDs) > 256 {
		return fmt.Errorf("table preferences: too many columns")
	}
	seen := map[string]bool{}
	for _, c := range p.Columns {
		if err := identifier("column id", c.ID, 256); err != nil {
			return err
		}
		if seen[c.ID] {
			return fmt.Errorf("table preferences: duplicate column id")
		}
		seen[c.ID] = true
		if c.Width != nil && (math.IsNaN(*c.Width) || math.IsInf(*c.Width, 0) || *c.Width < 24 || *c.Width > 4096) {
			return fmt.Errorf("table preferences: width must be between 24 and 4096")
		}
		if c.Align != "" && c.Align != "left" && c.Align != "center" && c.Align != "right" {
			return fmt.Errorf("table preferences: invalid alignment")
		}
		for _, text := range []struct {
			value *string
			max   int
		}{{c.DisplayName, 256}, {c.Tooltip, 1024}} {
			if text.value != nil && (!utf8.ValidString(*text.value) || utf8.RuneCountInString(*text.value) > text.max) {
				return fmt.Errorf("table preferences: text exceeds contract limits")
			}
		}
	}
	if p.Sort != nil {
		if err := identifier("sort column id", p.Sort.ColumnID, 256); err != nil {
			return err
		}
		if p.Sort.Direction != "asc" && p.Sort.Direction != "desc" {
			return fmt.Errorf("table preferences: invalid sort direction")
		}
	}
	if p.Density != "" && p.Density != "compact" && p.Density != "normal" {
		return fmt.Errorf("table preferences: invalid density")
	}
	seen = map[string]bool{}
	for _, id := range p.FrozenColumnIDs {
		if err := identifier("frozen column id", id, 256); err != nil {
			return err
		}
		if seen[id] {
			return fmt.Errorf("table preferences: duplicate frozen column id")
		}
		seen[id] = true
	}
	encoded, err := json.Marshal(p)
	if err != nil || len(encoded) > MaxBytes {
		return fmt.Errorf("table preferences: payload exceeds contract limits")
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	if len(data) > MaxBytes {
		return fmt.Errorf("table preferences: payload exceeds contract limits")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("table preferences: malformed payload: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("table preferences: trailing payload")
	}
	return nil
}

// Decode rejects runtime metadata, row values, and other unknown fields.
func Decode(data []byte) (*Preferences, error) {
	var p Preferences
	if err := decodeStrict(data, &p); err != nil {
		return nil, err
	}
	if err := Validate(&p); err != nil {
		return nil, err
	}
	return &p, nil
}
