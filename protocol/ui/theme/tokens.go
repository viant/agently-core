package theme

import (
	"fmt"
	"sort"
	"strings"
)

type tokenSpec struct {
	variable, applicationVariable, kind string
	min, max                            float64
}

var tokenSpecs = map[string]tokenSpec{
	"typography.family":     {variable: "--forge-font-family", applicationVariable: "--agently-theme-font-family", kind: "font"},
	"typography.size":       {variable: "--forge-font-size", applicationVariable: "--agently-theme-font-size", min: 8, max: 72},
	"surface":               {variable: "--forge-surface", applicationVariable: "--agently-theme-surface", kind: "color"},
	"text":                  {variable: "--forge-text", applicationVariable: "--agently-theme-text", kind: "color"},
	"control.minHeight":     {variable: "--forge-control-height", applicationVariable: "--agently-theme-control-height", min: 16, max: 128},
	"control.radius":        {variable: "--forge-control-radius", applicationVariable: "--agently-theme-control-radius", max: 64},
	"control.paddingInline": {variable: "--forge-control-padding-inline", applicationVariable: "--agently-theme-control-padding-inline", max: 64},
	"control.background":    {variable: "--forge-control-bg", applicationVariable: "--agently-theme-control-background", kind: "color"},
	"control.foreground":    {variable: "--forge-control-text", applicationVariable: "--agently-theme-control-foreground", kind: "color"},
	"control.border":        {variable: "--forge-control-border", applicationVariable: "--agently-theme-control-border", kind: "color"},
	"focus.color":           {variable: "--forge-focus-color", applicationVariable: "--agently-theme-focus", kind: "color"},
	"button.background":     {variable: "--forge-button-bg", applicationVariable: "--agently-theme-button-background", kind: "color"},
	"button.foreground":     {variable: "--forge-button-text", applicationVariable: "--agently-theme-button-foreground", kind: "color"},
	"disabled.background":   {variable: "--forge-disabled-bg", applicationVariable: "--agently-theme-disabled-background", kind: "color"},
	"disabled.foreground":   {variable: "--forge-disabled-text", applicationVariable: "--agently-theme-disabled-foreground", kind: "color"},
	"validation.border":     {variable: "--forge-invalid-border", applicationVariable: "--agently-theme-validation-border", kind: "color"},
}

var fontFamilies = map[string]string{
	"system":          "system-ui, sans-serif",
	"product-primary": "var(--agently-font-product-primary, system-ui, sans-serif)",
}

// Defaults returns a fresh palette. Dimensions are logical units, not CSS strings.
func Defaults(mode string) Tokens {
	values := Tokens{
		"typography.family": "system", "typography.size": 14,
		"control.minHeight": 36, "control.radius": 4, "control.paddingInline": 10,
		"surface": "#ffffff", "text": "#171b26", "control.background": "#ffffff",
		"control.foreground": "#171b26", "control.border": "#788397", "focus.color": "#4466cc",
		"button.background": "#3453b3", "button.foreground": "#ffffff",
		"disabled.background": "#edf0f5", "disabled.foreground": "#626d7e", "validation.border": "#b42318",
	}
	if mode == "dark" {
		for k, v := range (Tokens{
			"surface": "#1b2230", "text": "#edf1f7", "control.background": "#242d3d",
			"control.foreground": "#edf1f7", "control.border": "#8491a6", "focus.color": "#9db5ff",
			"button.background": "#a9bfff", "button.foreground": "#172447",
			"disabled.background": "#30394a", "disabled.foreground": "#a2adbf", "validation.border": "#ff9b91",
		}) {
			values[k] = v
		}
	}
	return values
}

// CSS emits only validated tokens. It does not load or compile workspace CSS.
// Passing a catalog not produced by Resolve still validates selector/value input.
func CSS(c *Catalog) (string, error) {
	if c == nil || c.Version != Version {
		return "", fmt.Errorf("unsupported theme catalog")
	}
	var out strings.Builder
	themes := append([]ResolvedTheme(nil), c.Themes...)
	sort.Slice(themes, func(i, j int) bool { return themes[i].ID < themes[j].ID })
	seen := map[string]bool{}
	for _, t := range themes {
		if !identifier.MatchString(t.ID) || t.ID == "forge-default" || seen[t.ID] {
			return "", fmt.Errorf("invalid theme ID")
		}
		seen[t.ID] = true
		for mode := range t.Modes {
			if mode != "light" && mode != "dark" {
				return "", fmt.Errorf("invalid mode")
			}
		}
		for _, mode := range []string{"light", "dark"} {
			values, ok := t.Modes[mode]
			if !ok {
				continue
			}
			if err := validateTokens(values); err != nil {
				return "", err
			}
			fmt.Fprintf(&out, ".agently-application[data-agently-theme=%q][data-agently-color-mode=%q] {\n", t.ID, mode)
			keys := make([]string, 0, len(values))
			for k := range values {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			rendered := make(map[string]string, len(keys))
			for _, k := range keys {
				spec := tokenSpecs[k]
				value := values[k]
				switch spec.kind {
				case "font":
					rendered[k] = fontFamilies[value.(string)]
				case "color":
					rendered[k] = value.(string)
				default:
					n, _ := number(value)
					rendered[k] = fmt.Sprintf("%gpx", n)
				}
				fmt.Fprintf(&out, "  %s: %s;\n", spec.applicationVariable, rendered[k])
			}
			out.WriteString("}\n")
			fmt.Fprintf(&out, ".agently-workspace[data-forge-theme=%q][data-forge-color-mode=%q] {\n", t.ID, mode)
			fmt.Fprintf(&out, "  color-scheme: %s;\n", mode)
			for _, k := range keys {
				spec := tokenSpecs[k]
				fmt.Fprintf(&out, "  %s: %s;\n", spec.variable, rendered[k])
			}
			out.WriteString("}\n")
		}
	}
	return out.String(), nil
}
