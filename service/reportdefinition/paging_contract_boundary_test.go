package reportdefinition

import (
	"os"
	"strings"
	"testing"
)

func TestReportDefinitionRejectsUnsupportedCursorContractInsteadOfIgnoringIt(t *testing.T) {
	original, err := os.ReadFile("testdata/report.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseStrict(original); err != nil {
		t.Fatal("original offset contract failed", err)
	}
	authored := strings.Replace(string(original), "  operations:\n", "  operations:\n    paging: {mode: cursor, parameters: {page: resumeToken}, dataInfoSelectors: {hasMore: remaining, nextCursor: nextToken}}\n", 1)
	if definition, err := ParseStrict([]byte(authored)); err == nil || definition != nil || !strings.Contains(err.Error(), "field paging not found") {
		t.Fatalf("unsupported cursor contract silently accepted: %#v %v", definition, err)
	}
}
