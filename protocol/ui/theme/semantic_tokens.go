package theme

// Optional roles extend the portable palette without changing baseline defaults.
// Workspaces opt in; components consume semantic variables, never theme literals.
func init() {
	colors := map[string]string{
		"canvas": "canvas", "surface.subtle": "surface-subtle", "surface.raised": "surface-raised",
		"text.secondary": "text-secondary", "text.muted": "text-muted", "text.inverse": "text-inverse",
		"border": "border", "border.strong": "border-strong",
		"interaction.foreground": "interaction", "interaction.hover": "interaction-hover",
		"interaction.active": "interaction-active", "interaction.selectedBackground": "selected-background",
	}
	for _, status := range []string{"info", "success", "warning", "danger"} {
		for _, role := range []string{"background", "foreground", "border"} {
			colors["status."+status+"."+role] = "status-" + status + "-" + role
		}
	}
	for _, index := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		colors["data.categorical."+index] = "data-categorical-" + index
	}
	for name, variable := range colors {
		tokenSpecs[name] = tokenSpec{variable: "--forge-" + variable, applicationVariable: "--agently-theme-" + variable, kind: "color"}
	}
	for _, role := range []string{"caption", "small", "body", "section", "heading", "title", "metric", "display", "code"} {
		tokenSpecs["typography."+role+".size"] = tokenSpec{variable: "--forge-type-" + role + "-size", applicationVariable: "--agently-theme-type-" + role + "-size", min: 8, max: 96}
		tokenSpecs["typography."+role+".lineHeight"] = tokenSpec{variable: "--forge-type-" + role + "-line-height", applicationVariable: "--agently-theme-type-" + role + "-line-height", min: 8, max: 144}
	}
}
