package workspace

import "testing"

func TestLayoutURLActions(t *testing.T) {
	for _, raw := range []string{"https://studio.example/?section=models", "http://localhost:8080/?section=tools", "http://127.0.0.1:8080/", "http://[::1]:8080/"} {
		if !validLayoutURL(raw) {
			t.Errorf("rejected %s", raw)
		}
	}
	for _, raw := range []string{"javascript:alert(1)", "data:text/html,hello", "http://studio.example/", "https://user:pass@studio.example/", "//studio.example/", " https://studio.example/", "https://studio.example\\@evil.example/", "https://studio.example/\n"} {
		if validLayoutURL(raw) {
			t.Errorf("accepted unsafe URL %q", raw)
		}
	}
	for _, action := range []*LayoutAction{{Type: "url", URL: "https://studio.example/?section=models"}, {Type: "window", ResourceURI: "window://studio/Models"}, {Type: "window", WindowKey: "models"}} {
		if err := validateMenus([]LayoutMenu{{ID: "test", Title: "Test", Action: action}}, map[string]bool{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range []*LayoutAction{{Type: "url", URL: "javascript:alert(1)"}, {Type: "window", ResourceURI: "report://studio/Delivery"}, {Type: "window", ResourceURI: "bareAlias"}, {Type: "window", ResourceURI: "window://studio/Models", WindowKey: "other"}, {Type: "url", URL: "https://studio.example/", WindowKey: "models"}} {
		if err := validateMenus([]LayoutMenu{{ID: "test", Title: "Test", Action: action}}, map[string]bool{}, nil); err == nil {
			t.Errorf("accepted invalid action %+v", action)
		}
	}
}
