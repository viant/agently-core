package reporting

import (
	"context"
	"encoding/json"
	"github.com/viant/agently-core/service/reporting/catalog"
	"testing"
)

func TestUnifiedCatalogHasNoLegacyAuthoringFallback(t *testing.T) {
	service := New(Options{Store: NewMemoryStore(), ReportCatalog: &catalog.ReportCatalogService{}})
	if _, err := service.SaveReport(context.Background(), &SaveReportRequest{ReportID: "legacy", ReportDocument: json.RawMessage(`{}`), ReportSpec: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("catalog-only service silently used legacy writer")
	}
	if _, err := service.DuplicateReport(context.Background(), &DuplicateReportRequest{ReportID: "legacy"}); err == nil {
		t.Fatal("catalog duplicate used legacy writer")
	}
	if _, err := service.DeleteReport(context.Background(), &DeleteReportRequest{ReportID: "legacy"}); err == nil {
		t.Fatal("catalog delete used legacy writer")
	}
}
