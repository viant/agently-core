package reportdefinition

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestActivatedMCPReportCarriesValidatedForgeArtifacts(t *testing.T) {
	definition, err := ParseStrict([]byte(strings.Replace(string(nativeAuthored(t)), "kind: ReportDefinition", "kind: reporting.report", 1)))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileBounded(context.Background(), definition, nil, DatasetFetcherFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		if request.DatasetID == "operationsEvidence" {
			return FetchResult{Rows: []map[string]any{{"region": "West", "processedUnits": 10, "operationsDate": "2026-09-01", "operatingCost": 5.5}}, HasMoreKnown: true}, nil
		}
		return FetchResult{HasMoreKnown: true}, nil
	}), 10)
	if err != nil {
		t.Fatal(err)
	}
	value := ActivatedReport{GroupID: "sample", ReportID: definition.Metadata.ID, ArtifactID: "artifact-1", Version: 3,
		DraftRevision: 2, ContentDigest: "digest", ReportDocument: compiled.ReportDocument,
		ReportSpec: compiled.ReportSpec, ReportFill: compiled.ReportFill, ReportPrint: compiled.ReportPrint, ComputedAt: time.Now().UTC()}
	envelope, err := json.Marshal(map[string]any{"status": "ok", "data": value})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeActivatedMCPReport(envelope, "sample", definition.Metadata.ID)
	if err != nil || decoded.ArtifactID != value.ArtifactID || len(decoded.ReportSpec) == 0 || len(decoded.ReportFill) == 0 {
		t.Fatalf("activated remote report=%+v err=%v", decoded, err)
	}
	if _, err := DecodeActivatedMCPReport(envelope, "other", definition.Metadata.ID); err == nil {
		t.Fatal("remote report from another group was accepted")
	}
	var fill map[string]any
	if err := json.Unmarshal(value.ReportFill, &fill); err != nil {
		t.Fatal(err)
	}
	fill["specVersion"] = 99
	value.ReportFill, _ = json.Marshal(fill)
	envelope, _ = json.Marshal(map[string]any{"status": "ok", "data": value})
	if _, err := DecodeActivatedMCPReport(envelope, "sample", definition.Metadata.ID); err == nil {
		t.Fatal("mismatched Forge versions were accepted")
	}
}
