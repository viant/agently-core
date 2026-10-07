package mcpapps

import "testing"

func TestVisibility(t *testing.T) {
	for _, tc := range []struct {
		name       string
		meta       map[string]interface{}
		model, app bool
	}{
		{"default", nil, true, true},
		{"resource defaults", map[string]interface{}{"ui": map[string]interface{}{"resourceUri": "ui://example/view"}}, true, true},
		{"app only", map[string]interface{}{"ui": map[string]interface{}{"visibility": []interface{}{"app"}}}, false, true},
		{"model only", map[string]interface{}{"ui": map[string]interface{}{"visibility": []string{"model"}}}, true, false},
		{"empty", map[string]interface{}{"ui": map[string]interface{}{"visibility": []string{}}}, false, false},
		{"malformed", map[string]interface{}{"ui": map[string]interface{}{"visibility": "app"}}, false, false},
		{"unknown", map[string]interface{}{"ui": map[string]interface{}{"visibility": []string{"app", "administrator"}}}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if Visible(tc.meta, "model") != tc.model || Visible(tc.meta, "app") != tc.app {
				t.Fatalf("visibility mismatch: %#v", tc.meta)
			}
		})
	}
}
