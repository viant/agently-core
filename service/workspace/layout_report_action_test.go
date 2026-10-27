package workspace

import "testing"

func TestReportLayoutAction(t *testing.T) {
	for _, uri := range []string{"report://workspace/summary", "report://workspace/details", "report://team/review", "report://team/activity"} {
		action := &LayoutAction{Type: "report", ResourceURI: uri, Parameters: map[string]any{"recordId": 0}}
		if err := validateMenus([]LayoutMenu{{ID: "reports", Title: "Reports", Action: action}}, map[string]bool{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range []*LayoutAction{
		{Type: "report"}, {Type: "report", ResourceURI: "window://workspace/summary"},
		{Type: "report", ResourceURI: "report://workspace/summary", WindowKey: "other"},
		{Type: "report", ResourceURI: "report://workspace/summary", URL: "https://example.invalid"},
		{Type: "report", ResourceURI: "report://workspace/summary", RefreshDataSources: []string{"other"}},
	} {
		if err := validateMenus([]LayoutMenu{{ID: "reports", Title: "Reports", Action: action}}, map[string]bool{}, nil); err == nil {
			t.Fatalf("accepted conflicting report action: %+v", action)
		}
	}
}
