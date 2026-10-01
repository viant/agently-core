// Package theme defines the portable workspace theme contract. It has no UI or
// filesystem dependency; web and native clients consume the same resolved maps.
//go:generate go run ./cmd/generate

package theme

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const Version = 1
const PaletteVersion = 1
const MaxManifestBytes = 512 * 1024

const maxFontFamilies = 4
const maxFontFaces = 32

type Tokens map[string]interface{}
type FontFace struct {
	File         string `json:"file" yaml:"file"`
	Style        string `json:"style" yaml:"style"`
	Weight       string `json:"weight" yaml:"weight"`
	UnicodeRange string `json:"unicodeRange,omitempty" yaml:"unicodeRange,omitempty"`
}
type FontFamily struct {
	Role     string     `json:"role" yaml:"role"`
	Name     string     `json:"name" yaml:"name"`
	Fallback string     `json:"fallback,omitempty" yaml:"fallback,omitempty"`
	Faces    []FontFace `json:"faces" yaml:"faces"`
}
type Variant struct {
	Tokens Tokens   `json:"tokens,omitempty" yaml:"tokens,omitempty"`
	Files  []string `json:"files,omitempty" yaml:"files,omitempty"`
}
type Theme struct {
	ID           string             `json:"id" yaml:"id"`
	Label        string             `json:"label" yaml:"label"`
	FallbackMode string             `json:"fallbackMode" yaml:"fallbackMode"`
	Tokens       Tokens             `json:"tokens,omitempty" yaml:"tokens,omitempty"`
	Files        []string           `json:"files,omitempty" yaml:"files,omitempty"`
	Modes        map[string]Variant `json:"modes" yaml:"modes"`
}
type Manifest struct {
	Overrides    []string     `json:"overrides,omitempty" yaml:"overrides,omitempty"`
	Version      int          `json:"version" yaml:"version"`
	Fonts        []FontFamily `json:"fonts,omitempty" yaml:"fonts,omitempty"`
	Files        []string     `json:"files,omitempty" yaml:"files,omitempty"`
	DefaultTheme string       `json:"defaultTheme,omitempty" yaml:"defaultTheme,omitempty"`
	DefaultMode  string       `json:"defaultMode,omitempty" yaml:"defaultMode,omitempty"`
	Themes       []Theme      `json:"themes,omitempty" yaml:"themes,omitempty"`
}
type ResolvedTheme struct {
	ID           string            `json:"id"`
	Label        string            `json:"label"`
	FallbackMode string            `json:"fallbackMode"`
	Modes        map[string]Tokens `json:"modes"`
}
type Catalog struct {
	Version        int             `json:"version"`
	PaletteVersion int             `json:"paletteVersion"`
	DefaultTheme   string          `json:"defaultTheme"`
	DefaultMode    string          `json:"defaultMode"`
	Themes         []ResolvedTheme `json:"themes"`
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var color = regexp.MustCompile(`^#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?$`)
var fontName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,79}$`)
var unicodeRange = regexp.MustCompile(`^U\+[0-9A-F?]{1,6}(-[0-9A-F]{1,6})?(,U\+[0-9A-F?]{1,6}(-[0-9A-F]{1,6})?)*$`)

// Parse rejects unknown fields, duplicate YAML keys, and multiple documents.
func Parse(data []byte) (*Manifest, error) {
	if len(data) > MaxManifestBytes {
		return nil, fmt.Errorf("theme manifest exceeds %d bytes", MaxManifestBytes)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("theme manifest must contain exactly one document")
	}
	if _, err := Resolve(m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Resolve returns independently allocated, complete token maps in stable ID order.
// Asset path/content validation is performed by the workspace asset loader.
func Resolve(m Manifest) (*Catalog, error) {
	if m.Version != Version {
		return nil, fmt.Errorf("unsupported theme version %d", m.Version)
	}
	mode := m.DefaultMode
	if mode == "" {
		mode = "system"
	}
	if mode != "light" && mode != "dark" && mode != "system" {
		return nil, fmt.Errorf("invalid defaultMode %q", mode)
	}
	if len(m.Themes) > 32 {
		return nil, fmt.Errorf("at most 32 themes are supported")
	}
	registeredFonts, err := validateFonts(m.Fonts)
	if err != nil {
		return nil, err
	}
	result := &Catalog{Version: Version, PaletteVersion: PaletteVersion, DefaultTheme: m.DefaultTheme, DefaultMode: mode, Themes: []ResolvedTheme{}}
	seen := map[string]bool{}
	for _, t := range m.Themes {
		if !identifier.MatchString(t.ID) || t.ID == "forge-default" || seen[t.ID] {
			return nil, fmt.Errorf("invalid or duplicate theme ID %q", t.ID)
		}
		seen[t.ID] = true
		if strings.TrimSpace(t.Label) == "" || len(t.Label) > 256 {
			return nil, fmt.Errorf("theme %s requires a label of at most 256 bytes", t.ID)
		}
		if len(t.Modes) == 0 {
			return nil, fmt.Errorf("theme %s has no modes", t.ID)
		}
		if _, ok := t.Modes[t.FallbackMode]; !ok {
			return nil, fmt.Errorf("theme %s has unsupported fallbackMode", t.ID)
		}
		if err := validateTokens(t.Tokens); err != nil {
			return nil, fmt.Errorf("theme %s: %w", t.ID, err)
		}
		resolved := ResolvedTheme{ID: t.ID, Label: t.Label, FallbackMode: t.FallbackMode, Modes: map[string]Tokens{}}
		for mode, variant := range t.Modes {
			if mode != "light" && mode != "dark" {
				return nil, fmt.Errorf("theme %s has invalid mode %q", t.ID, mode)
			}
			if err := validateTokens(variant.Tokens); err != nil {
				return nil, fmt.Errorf("theme %s/%s: %w", t.ID, mode, err)
			}
			values := Defaults(mode)
			for k, v := range t.Tokens {
				values[k] = v
			}
			for k, v := range variant.Tokens {
				values[k] = v
			}
			if family, _ := values["typography.family"].(string); family != "system" && !registeredFonts[family] {
				return nil, fmt.Errorf("theme %s/%s selects unregistered font role %q", t.ID, mode, family)
			}
			resolved.Modes[mode] = values
		}
		result.Themes = append(result.Themes, resolved)
	}
	if len(m.Themes) > 0 && !seen[m.DefaultTheme] {
		return nil, fmt.Errorf("defaultTheme must reference a declared theme")
	}
	if len(m.Themes) == 0 && m.DefaultTheme != "" {
		return nil, fmt.Errorf("defaultTheme requires themes")
	}
	sort.Slice(result.Themes, func(i, j int) bool { return result.Themes[i].ID < result.Themes[j].ID })
	return result, nil
}

func validateFonts(fonts []FontFamily) (map[string]bool, error) {
	if len(fonts) > maxFontFamilies {
		return nil, fmt.Errorf("at most %d font families are supported", maxFontFamilies)
	}
	roles := make(map[string]bool, len(fonts))
	faces := 0
	for _, family := range fonts {
		if family.Role == "system" || fontFamilies[family.Role] == "" || roles[family.Role] {
			return nil, fmt.Errorf("invalid or duplicate font role %q", family.Role)
		}
		if !fontName.MatchString(family.Name) {
			return nil, fmt.Errorf("font role %s requires a safe family name", family.Role)
		}
		if family.Fallback != "" && family.Fallback != "system" && family.Fallback != "sans-serif" && family.Fallback != "serif" && family.Fallback != "monospace" {
			return nil, fmt.Errorf("font role %s has unsupported fallback %q", family.Role, family.Fallback)
		}
		if len(family.Faces) == 0 {
			return nil, fmt.Errorf("font role %s requires at least one face", family.Role)
		}
		faces += len(family.Faces)
		if faces > maxFontFaces {
			return nil, fmt.Errorf("at most %d font faces are supported", maxFontFaces)
		}
		seen := map[string]bool{}
		for _, face := range family.Faces {
			if face.Style != "normal" && face.Style != "italic" {
				return nil, fmt.Errorf("font role %s has unsupported style %q", family.Role, face.Style)
			}
			weight, ok := validFontWeight(face.Weight)
			if !ok {
				return nil, fmt.Errorf("font role %s has invalid weight %q", family.Role, face.Weight)
			}
			if face.UnicodeRange != "" && !unicodeRange.MatchString(face.UnicodeRange) {
				return nil, fmt.Errorf("font role %s has invalid unicode range", family.Role)
			}
			key := strings.Join([]string{face.Style, weight, face.UnicodeRange}, "\x00")
			if seen[key] {
				return nil, fmt.Errorf("font role %s has a duplicate face", family.Role)
			}
			seen[key] = true
		}
		roles[family.Role] = true
	}
	return roles, nil
}

func validFontWeight(value string) (string, bool) {
	parts := strings.Fields(value)
	if len(parts) < 1 || len(parts) > 2 {
		return "", false
	}
	weights := make([]int, len(parts))
	for i, part := range parts {
		var weight int
		if _, err := fmt.Sscanf(part, "%d", &weight); err != nil || fmt.Sprintf("%d", weight) != part || weight < 1 || weight > 1000 {
			return "", false
		}
		weights[i] = weight
	}
	if len(weights) == 2 && weights[0] > weights[1] {
		return "", false
	}
	return strings.Join(parts, " "), true
}

func validateTokens(tokens Tokens) error {
	for key, value := range tokens {
		spec, ok := tokenSpecs[key]
		if !ok {
			return fmt.Errorf("unknown token %q", key)
		}
		switch spec.kind {
		case "color":
			v, ok := value.(string)
			if !ok || !color.MatchString(v) {
				return fmt.Errorf("token %s requires a hex color", key)
			}
		case "font":
			v, ok := value.(string)
			if _, supported := fontFamilies[v]; !ok || !supported {
				return fmt.Errorf("token %s requires a supported font family", key)
			}
		default:
			n, ok := number(value)
			if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < spec.min || n > spec.max {
				return fmt.Errorf("token %s requires a number between %g and %g", key, spec.min, spec.max)
			}
		}
	}
	return nil
}
func number(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, e := n.Float64()
		return f, e == nil
	}
	return 0, false
}

// EffectiveMode implements the same fallback rule expected of all clients.
func (t ResolvedTheme) EffectiveMode(preference, systemMode string) string {
	mode := preference
	if mode == "system" {
		mode = systemMode
	}
	if _, ok := t.Modes[mode]; ok {
		return mode
	}
	return t.FallbackMode
}
