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

type Tokens map[string]interface{}
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
	Overrides    []string `json:"overrides,omitempty" yaml:"overrides,omitempty"`
	Version      int      `json:"version" yaml:"version"`
	Files        []string `json:"files,omitempty" yaml:"files,omitempty"`
	DefaultTheme string   `json:"defaultTheme,omitempty" yaml:"defaultTheme,omitempty"`
	DefaultMode  string   `json:"defaultMode,omitempty" yaml:"defaultMode,omitempty"`
	Themes       []Theme  `json:"themes,omitempty" yaml:"themes,omitempty"`
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
			if v, ok := value.(string); !ok || v != "system" {
				return fmt.Errorf("token %s supports only system", key)
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
