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

var tokenSpecs = makeTokenSpecs()

func makeTokenSpecs() map[string]tokenSpec {
	specs := map[string]tokenSpec{
		"typography.family":              {variable: "--forge-font-family", applicationVariable: "--agently-theme-font-family", kind: "font"},
		"typography.size":                {variable: "--forge-font-size", applicationVariable: "--agently-theme-font-size", min: 8, max: 96},
		"canvas":                         {variable: "--forge-canvas", applicationVariable: "--agently-theme-canvas", kind: "color"},
		"surface":                        {variable: "--forge-surface", applicationVariable: "--agently-theme-surface", kind: "color"},
		"surface.subtle":                 {variable: "--forge-surface-subtle", applicationVariable: "--agently-theme-surface-subtle", kind: "color"},
		"surface.raised":                 {variable: "--forge-surface-raised", applicationVariable: "--agently-theme-surface-raised", kind: "color"},
		"text":                           {variable: "--forge-text", applicationVariable: "--agently-theme-text", kind: "color"},
		"text.secondary":                 {variable: "--forge-text-secondary", applicationVariable: "--agently-theme-text-secondary", kind: "color"},
		"text.muted":                     {variable: "--forge-text-muted", applicationVariable: "--agently-theme-text-muted", kind: "color"},
		"text.inverse":                   {variable: "--forge-text-inverse", applicationVariable: "--agently-theme-text-inverse", kind: "color"},
		"border":                         {variable: "--forge-border-color", applicationVariable: "--agently-theme-border", kind: "color"},
		"border.strong":                  {variable: "--forge-border-strong", applicationVariable: "--agently-theme-border-strong", kind: "color"},
		"interaction.foreground":         {variable: "--forge-interaction-color", applicationVariable: "--agently-theme-interaction", kind: "color"},
		"interaction.hover":              {variable: "--forge-interaction-hover-color", applicationVariable: "--agently-theme-interaction-hover", kind: "color"},
		"interaction.active":             {variable: "--forge-interaction-active-color", applicationVariable: "--agently-theme-interaction-active", kind: "color"},
		"interaction.selectedBackground": {variable: "--forge-selected-background", applicationVariable: "--agently-theme-selected-background", kind: "color"},
		"control.minHeight":              {variable: "--forge-control-height", applicationVariable: "--agently-theme-control-height", min: 16, max: 128},
		"control.radius":                 {variable: "--forge-control-radius", applicationVariable: "--agently-theme-control-radius", max: 64},
		"control.paddingInline":          {variable: "--forge-control-padding-inline", applicationVariable: "--agently-theme-control-padding-inline", max: 64},
		"control.background":             {variable: "--forge-control-bg", applicationVariable: "--agently-theme-control-background", kind: "color"},
		"control.foreground":             {variable: "--forge-control-text", applicationVariable: "--agently-theme-control-foreground", kind: "color"},
		"control.border":                 {variable: "--forge-control-border", applicationVariable: "--agently-theme-control-border", kind: "color"},
		"focus.color":                    {variable: "--forge-focus-color", applicationVariable: "--agently-theme-focus", kind: "color"},
		"button.background":              {variable: "--forge-button-bg", applicationVariable: "--agently-theme-button-background", kind: "color"},
		"button.foreground":              {variable: "--forge-button-text", applicationVariable: "--agently-theme-button-foreground", kind: "color"},
		"disabled.background":            {variable: "--forge-disabled-bg", applicationVariable: "--agently-theme-disabled-background", kind: "color"},
		"disabled.foreground":            {variable: "--forge-disabled-text", applicationVariable: "--agently-theme-disabled-foreground", kind: "color"},
		"validation.border":              {variable: "--forge-invalid-border", applicationVariable: "--agently-theme-validation-border", kind: "color"},
		"status.info.background":         {variable: "--forge-status-info-background", applicationVariable: "--agently-theme-status-info-background", kind: "color"},
		"status.info.foreground":         {variable: "--forge-status-info-foreground", applicationVariable: "--agently-theme-status-info-foreground", kind: "color"},
		"status.info.border":             {variable: "--forge-status-info-border", applicationVariable: "--agently-theme-status-info-border", kind: "color"},
		"status.success.background":      {variable: "--forge-status-success-background", applicationVariable: "--agently-theme-status-success-background", kind: "color"},
		"status.success.foreground":      {variable: "--forge-status-success-foreground", applicationVariable: "--agently-theme-status-success-foreground", kind: "color"},
		"status.success.border":          {variable: "--forge-status-success-border", applicationVariable: "--agently-theme-status-success-border", kind: "color"},
		"status.warning.background":      {variable: "--forge-status-warning-background", applicationVariable: "--agently-theme-status-warning-background", kind: "color"},
		"status.warning.foreground":      {variable: "--forge-status-warning-foreground", applicationVariable: "--agently-theme-status-warning-foreground", kind: "color"},
		"status.warning.border":          {variable: "--forge-status-warning-border", applicationVariable: "--agently-theme-status-warning-border", kind: "color"},
		"status.danger.background":       {variable: "--forge-status-danger-background", applicationVariable: "--agently-theme-status-danger-background", kind: "color"},
		"status.danger.foreground":       {variable: "--forge-status-danger-foreground", applicationVariable: "--agently-theme-status-danger-foreground", kind: "color"},
		"status.danger.border":           {variable: "--forge-status-danger-border", applicationVariable: "--agently-theme-status-danger-border", kind: "color"},
		"data.categorical.1":             {variable: "--forge-data-categorical-1", applicationVariable: "--agently-theme-data-categorical-1", kind: "color"},
		"data.categorical.2":             {variable: "--forge-data-categorical-2", applicationVariable: "--agently-theme-data-categorical-2", kind: "color"},
		"data.categorical.3":             {variable: "--forge-data-categorical-3", applicationVariable: "--agently-theme-data-categorical-3", kind: "color"},
		"data.categorical.4":             {variable: "--forge-data-categorical-4", applicationVariable: "--agently-theme-data-categorical-4", kind: "color"},
		"data.categorical.5":             {variable: "--forge-data-categorical-5", applicationVariable: "--agently-theme-data-categorical-5", kind: "color"},
		"data.categorical.6":             {variable: "--forge-data-categorical-6", applicationVariable: "--agently-theme-data-categorical-6", kind: "color"},
		"data.sequential.1":              {variable: "--forge-data-sequential-1", applicationVariable: "--agently-theme-data-sequential-1", kind: "color"},
		"data.sequential.2":              {variable: "--forge-data-sequential-2", applicationVariable: "--agently-theme-data-sequential-2", kind: "color"},
		"data.sequential.3":              {variable: "--forge-data-sequential-3", applicationVariable: "--agently-theme-data-sequential-3", kind: "color"},
		"data.sequential.4":              {variable: "--forge-data-sequential-4", applicationVariable: "--agently-theme-data-sequential-4", kind: "color"},
		"data.sequential.5":              {variable: "--forge-data-sequential-5", applicationVariable: "--agently-theme-data-sequential-5", kind: "color"},
	}
	for _, role := range []string{"caption", "small", "body", "section", "heading", "title", "metric", "display", "code", "label", "metadata"} {
		prefix := "typography." + role + "."
		variable := "--forge-type-" + role + "-"
		application := "--agently-theme-type-" + role + "-"
		specs[prefix+"family"] = tokenSpec{variable: variable + "family", applicationVariable: application + "family", kind: "font"}
		specs[prefix+"size"] = tokenSpec{variable: variable + "size", applicationVariable: application + "size", min: 8, max: 96}
		specs[prefix+"lineHeight"] = tokenSpec{variable: variable + "line-height", applicationVariable: application + "line-height", min: 8, max: 144}
		specs[prefix+"weight"] = tokenSpec{variable: variable + "weight", applicationVariable: application + "weight", kind: "weight", min: 100, max: 900}
		specs[prefix+"letterSpacing"] = tokenSpec{variable: variable + "letter-spacing", applicationVariable: application + "letter-spacing", kind: "em", min: -0.04, max: 0.2}
	}
	specs["spacing.1"] = tokenSpec{variable: "--forge-spacing-1", applicationVariable: "--agently-theme-spacing-1", min: 0, max: 64}
	specs["spacing.2"] = tokenSpec{variable: "--forge-spacing-2", applicationVariable: "--agently-theme-spacing-2", min: 0, max: 64}
	specs["spacing.3"] = tokenSpec{variable: "--forge-spacing-3", applicationVariable: "--agently-theme-spacing-3", min: 0, max: 64}
	specs["spacing.4"] = tokenSpec{variable: "--forge-spacing-4", applicationVariable: "--agently-theme-spacing-4", min: 0, max: 64}
	specs["spacing.5"] = tokenSpec{variable: "--forge-spacing-5", applicationVariable: "--agently-theme-spacing-5", min: 0, max: 64}
	specs["spacing.6"] = tokenSpec{variable: "--forge-spacing-6", applicationVariable: "--agently-theme-spacing-6", min: 0, max: 64}
	specs["border.width"] = tokenSpec{variable: "--forge-border-width", applicationVariable: "--agently-theme-border-width", min: 0, max: 4}
	specs["radius.surface"] = tokenSpec{variable: "--forge-radius-surface", applicationVariable: "--agently-theme-radius-surface", min: 0, max: 32}
	specs["radius.pill"] = tokenSpec{variable: "--forge-radius-pill", applicationVariable: "--agently-theme-radius-pill", min: 0, max: 999}
	specs["focus.width"] = tokenSpec{variable: "--forge-focus-width", applicationVariable: "--agently-theme-focus-width", min: 1, max: 4}
	specs["focus.offset"] = tokenSpec{variable: "--forge-focus-offset", applicationVariable: "--agently-theme-focus-offset", min: 0, max: 8}
	specs["motion.fast"] = tokenSpec{variable: "--forge-motion-fast", applicationVariable: "--agently-theme-motion-fast", kind: "ms", min: 0, max: 500}
	specs["icon.size"] = tokenSpec{variable: "--forge-icon-size", applicationVariable: "--agently-theme-icon-size", min: 12, max: 32}
	return specs
}

var fontFamilies = map[string]string{
	"system":              "system-ui, sans-serif",
	"monospace":           "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
	"workspace-primary":   "var(--agently-font-workspace-primary, system-ui, sans-serif)",
	"workspace-secondary": "var(--agently-font-workspace-secondary, system-ui, sans-serif)",
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
// Profiles supply the full catalog when the style service emits one theme at a time.
func CSS(c *Catalog, profiles ...ResolvedTheme) (string, error) {
	if c == nil || c.Version != Version {
		return "", fmt.Errorf("unsupported theme catalog")
	}
	available := append(append([]ResolvedTheme{}, c.Themes...), profiles...)
	if err := validatePresentations(&Catalog{Themes: available}); err != nil {
		return "", err
	}
	themes := append([]ResolvedTheme(nil), c.Themes...)
	sort.Slice(themes, func(i, j int) bool { return themes[i].ID < themes[j].ID })
	var out strings.Builder
	seen := map[string]bool{}
	for _, base := range themes {
		if !identifier.MatchString(base.ID) || base.ID == "forge-default" || seen[base.ID] {
			return "", fmt.Errorf("invalid theme ID")
		}
		seen[base.ID] = true
		for mode := range base.Modes {
			if mode != "light" && mode != "dark" {
				return "", fmt.Errorf("invalid mode")
			}
		}
		for _, mode := range []string{"light", "dark"} {
			values, ok := base.Modes[mode]
			if !ok {
				continue
			}
			app := fmt.Sprintf(".agently-application[data-agently-theme=%q][data-agently-color-mode=%q]", base.ID, mode)
			forge := fmt.Sprintf(".agently-workspace[data-forge-theme=%q][data-forge-color-mode=%q]", base.ID, mode)
			if err := writeTokenCSS(&out, values, mode, app, forge); err != nil {
				return "", err
			}
			profileID := base.Presentations["document"]
			if profileID == "" {
				continue
			}
			var profile ResolvedTheme
			for _, candidate := range available {
				if candidate.ID == profileID {
					profile = candidate
					break
				}
			}
			profileMode := mode
			scoped, ok := profile.Modes[profileMode]
			if !ok {
				profileMode = profile.FallbackMode
				scoped, ok = profile.Modes[profileMode]
			}
			if !ok {
				return "", fmt.Errorf("presentation profile %s has no fallback mode", profileID)
			}
			if err := writeTokenCSS(&out, scoped, profileMode, app+` [data-agently-presentation="document"]`, forge+`[data-forge-presentation="document"]`); err != nil {
				return "", err
			}
			// An unrelated destination restores the selected theme inside the migrated shell.
			if err := writeTokenCSS(&out, values, mode, app+` [data-agently-presentation="legacy"]`, ""); err != nil {
				return "", err
			}
		}
	}
	return out.String(), nil
}

func writeTokenCSS(out *strings.Builder, values Tokens, mode, applicationSelector, forgeSelector string) error {
	if mode != "light" && mode != "dark" {
		return fmt.Errorf("invalid mode")
	}
	if err := validateTokens(values); err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rendered := make(map[string]string, len(keys))
	for _, key := range keys {
		spec, value := tokenSpecs[key], values[key]
		switch spec.kind {
		case "font":
			rendered[key] = fontFamilies[value.(string)]
		case "weight":
			n, _ := number(value)
			rendered[key] = fmt.Sprintf("%g", n)
		case "ms":
			n, _ := number(value)
			rendered[key] = fmt.Sprintf("%gms", n)
		case "em":
			n, _ := number(value)
			rendered[key] = fmt.Sprintf("%gem", n)
		case "color":
			rendered[key] = value.(string)
		default:
			n, _ := number(value)
			rendered[key] = fmt.Sprintf("%gpx", n)
		}
	}
	if applicationSelector != "" {
		fmt.Fprintf(out, "%s {\n", applicationSelector)
		for _, key := range keys {
			fmt.Fprintf(out, "  %s: %s;\n", tokenSpecs[key].applicationVariable, rendered[key])
		}
		out.WriteString("}\n")
	}
	if forgeSelector != "" {
		fmt.Fprintf(out, "%s {\n  color-scheme: %s;\n", forgeSelector, mode)
		for _, key := range keys {
			fmt.Fprintf(out, "  %s: %s;\n", tokenSpecs[key].variable, rendered[key])
			if alias := map[string]string{"border": "--forge-border", "interaction.foreground": "--forge-interaction", "interaction.hover": "--forge-interaction-hover", "interaction.active": "--forge-interaction-active"}[key]; alias != "" {
				fmt.Fprintf(out, "  %s: %s;\n", alias, rendered[key])
			}
		}
		out.WriteString("}\n")
	}
	return nil
}
