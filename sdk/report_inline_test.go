package sdk

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeRenderedContent_AssemblesProgressiveDashboardReport(t *testing.T) {
	content := "```forge-data\n" +
		`{"version":2,"scope":"campaign","reportRef":"brief","id":"summary","sequence":1,"format":"json","mode":"replace","data":[{"spend":12}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"scope":"campaign","id":"brief","sequence":2,"mode":"start","title":"Delivery","blocks":[{"id":"summary-card","kind":"dashboard.summary","dataSourceRef":"summary"}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"scope":"campaign","id":"brief","sequence":3,"mode":"append","blocks":[{"id":"detail-table","kind":"dashboard.table","dataSourceRef":"summary"}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"scope":"campaign","id":"brief","sequence":4,"mode":"commit"}` +
		"\n```"

	got := NormalizeRenderedContent(content)
	require.NotNil(t, got)
	require.Len(t, got.Reports, 1)
	report := got.Reports[0]
	require.Equal(t, "campaign", report.Scope)
	require.Equal(t, "brief", report.ID)
	require.Equal(t, "dashboard-v1", report.Grammar)
	require.Equal(t, "committed", report.Status)
	require.Equal(t, 4, report.Sequence)
	require.Contains(t, report.DataSources, "summary")

	var source map[string]interface{}
	require.NoError(t, json.Unmarshal(report.Source, &source))
	require.Equal(t, "Delivery", source["title"])
	require.Len(t, source["blocks"], 2)
	require.Empty(t, got.Diagnostics)
}

func TestNormalizeRenderedContent_DoesNotCommitAfterRejectedReportFragment(t *testing.T) {
	content := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","grammar":"dashboard-v1","blocks":[]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":2,"mode":"append","blocks":[{"kind":"dashboard.report","title":"Missing stable id"}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":3,"mode":"commit"}` +
		"\n```"

	got := NormalizeRenderedContent(content)
	require.Len(t, got.Reports, 1)
	require.Equal(t, "incomplete", got.Reports[0].Status)
	require.Contains(t, warningCodes(got.Diagnostics), "REPORT_TRANSACTION_INVALID")
	require.Contains(t, warningCodes(got.Diagnostics), "REPORT_SEQUENCE_GAP")
}

func TestNormalizeRenderedContent_KeepsLegacyForgeNamespacesSeparate(t *testing.T) {
	content := "```forge-data\n" +
		`{"id":"rows","data":[{"legacy":true}]}` +
		"\n```\n```forge-ui\n" +
		`{"blocks":[]}` +
		"\n```\n```forge-data\n" +
		`{"version":2,"reportRef":"brief","id":"rows","sequence":1,"data":[{"report":true}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":2,"mode":"start","blocks":[]}` +
		"\n```"

	got := NormalizeRenderedContent(content)
	require.Len(t, got.Reports, 1)
	require.JSONEq(t, `[{"report":true}]`, string(got.Reports[0].DataSources["rows"].Payload))
	require.Equal(t, "forgeData", got.Parts[0].Kind)
	require.Equal(t, 0, got.Parts[0].Data.Version)
	require.Equal(t, "forgeUI", got.Parts[2].Kind)
}

func TestNormalizeRenderedContent_RequiresVersionTwoForProgressiveData(t *testing.T) {
	content := NormalizeRenderedContent("```forge-data\n" +
		`{"version":1,"reportRef":"brief","id":"rows","sequence":1,"data":[]}` + "\n```\n" +
		"```forge-report\n" + `{"version":1,"id":"brief","sequence":2,"mode":"start","blocks":[]}` + "\n```")
	require.Len(t, content.Reports, 1)
	require.Empty(t, content.Reports[0].DataSources)
	require.Contains(t, warningCodes(content.Diagnostics), "REPORT_DATA_VERSION_REQUIRED")
}

func TestNormalizeRenderedContent_RejectsDashboardNestedTargetAtomically(t *testing.T) {
	content := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[{"id":"summary","kind":"dashboard.summary"}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":2,"mode":"append","target":{"kind":"block","ref":"summary","slot":"children"},"blocks":[{"id":"nested","kind":"dashboard.table"}]}` +
		"\n```"

	got := NormalizeRenderedContent(content)
	require.Len(t, got.Reports, 1)
	var source map[string]interface{}
	require.NoError(t, json.Unmarshal(got.Reports[0].Source, &source))
	require.Len(t, source["blocks"], 1)
	require.Contains(t, warningCodes(got.Diagnostics), "REPORT_TRANSACTION_INVALID")
}

func TestNormalizeRenderedContent_AppendsCanonicalNestedTarget(t *testing.T) {
	content := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","grammar":"report-document-v1","blocks":[{"id":"inventory","kind":"compositeBlock","childBlockIds":[]}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":2,"mode":"append","target":{"kind":"block","ref":"inventory","slot":"childBlockIds","position":"append"},"blocks":[{"id":"inventory-table","kind":"tableBlock"}]}` +
		"\n```"

	got := NormalizeRenderedContent(content)
	require.Len(t, got.Reports, 1)
	var source struct {
		Blocks []struct {
			ID            string   `json:"id"`
			ChildBlockIDs []string `json:"childBlockIds"`
		} `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal(got.Reports[0].Source, &source))
	require.Len(t, source.Blocks, 2)
	require.Equal(t, []string{"inventory-table"}, source.Blocks[0].ChildBlockIDs)
	require.Equal(t, "inventory-table", source.Blocks[1].ID)
}

func TestNormalizeRenderedContent_AppendsCanonicalTabGroupTarget(t *testing.T) {
	content := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","grammar":"report-document-v1","blocks":[{"id":"views","kind":"tabGroupBlock","sectionIds":[]}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":2,"mode":"append","target":{"kind":"block","ref":"views","slot":"sectionIds","position":"append"},"blocks":[{"id":"delivery-section","kind":"sectionBlock"}]}` +
		"\n```"

	got := NormalizeRenderedContent(content)
	require.Empty(t, got.Diagnostics)
	require.Len(t, got.Reports, 1)
	var source struct {
		Blocks []struct {
			ID         string   `json:"id"`
			SectionIDs []string `json:"sectionIds"`
		} `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal(got.Reports[0].Source, &source))
	require.Len(t, source.Blocks, 2)
	require.Equal(t, []string{"delivery-section"}, source.Blocks[0].SectionIDs)
	require.Equal(t, "delivery-section", source.Blocks[1].ID)
}

func TestNormalizeRenderedContent_DetectsReplayConflictAndGap(t *testing.T) {
	content := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","title":"different","blocks":[]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":3,"mode":"commit"}` +
		"\n```"

	got := NormalizeRenderedContent(content)
	require.Equal(t, "incomplete", got.Reports[0].Status)
	codes := warningCodes(got.Diagnostics)
	require.Contains(t, codes, "REPORT_SEQUENCE_CONFLICT")
	require.Contains(t, codes, "REPORT_TRANSACTION_INVALID")
	require.Contains(t, codes, "REPORT_SEQUENCE_GAP")
}

func TestNormalizeRenderedContent_ReportAppendReplayIsIdempotent(t *testing.T) {
	content := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":2,"mode":"append","blocks":[{"id":"table","kind":"dashboard.table"}]}` +
		"\n```\n```forge-report\n" +
		`{ "mode":"append", "blocks":[{"kind":"dashboard.table","id":"table"}], "sequence":2, "id":"brief", "version":1 }` +
		"\n```"

	got := NormalizeRenderedContent(content)
	require.Len(t, got.Reports, 1)
	var source struct {
		Blocks []json.RawMessage `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal(got.Reports[0].Source, &source))
	require.Len(t, source.Blocks, 1)
	require.NotContains(t, warningCodes(got.Diagnostics), "REPORT_SEQUENCE_CONFLICT")
}

func TestNormalizeRenderedContent_RejectsDuplicateBlockIDWithoutLosingSnapshot(t *testing.T) {
	content := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[{"id":"table","kind":"dashboard.table"}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":2,"mode":"append","blocks":[{"id":"table","kind":"dashboard.table","title":"Duplicate"}]}` +
		"\n```"

	got := NormalizeRenderedContent(content)
	var source struct {
		Blocks []struct {
			Title string `json:"title"`
		} `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal(got.Reports[0].Source, &source))
	require.Len(t, source.Blocks, 1)
	require.Empty(t, source.Blocks[0].Title)
	require.Contains(t, warningCodes(got.Diagnostics), "REPORT_TRANSACTION_INVALID")
}

func TestNormalizeRenderedContent_IsolatesSharedWorkspaceRefByReportInstance(t *testing.T) {
	content := "```forge-report\n" +
		`{"version":1,"scope":"shared","id":"one","sequence":1,"mode":"start","datasets":[{"id":"delivery","kind":"workspaceRef","dataSourceRef":"metrics"}],"blocks":[{"id":"one_table","kind":"dashboard.table","dataSourceRef":"delivery"}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"scope":"shared","id":"two","sequence":1,"mode":"start","datasets":[{"id":"delivery","kind":"workspaceRef","dataSourceRef":"metrics"}],"blocks":[{"id":"two_table","kind":"dashboard.table","dataSourceRef":"delivery"}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"scope":"shared","id":"one","sequence":2,"mode":"replace","grammar":"dashboard-v1","datasets":[{"id":"delivery","kind":"workspaceRef","dataSourceRef":"metrics"}],"blocks":[{"id":"one_replaced","kind":"dashboard.table","dataSourceRef":"delivery"}]}` +
		"\n```"

	got := NormalizeRenderedContent(content)
	require.Len(t, got.Reports, 2)
	require.Equal(t, "one", got.Reports[0].ID)
	require.Equal(t, 1, got.Reports[0].ResetVersion)
	require.Equal(t, "two", got.Reports[1].ID)
	require.Equal(t, 0, got.Reports[1].ResetVersion)
	require.NotEqual(t, string(got.Reports[0].Source), string(got.Reports[1].Source))
}

func TestNormalizeRenderedContent_RejectsPostCommitTransaction(t *testing.T) {
	content := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":2,"mode":"commit"}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":3,"mode":"append","blocks":[{"id":"late","kind":"dashboard.table"}]}` +
		"\n```"

	got := NormalizeRenderedContent(content)
	require.Equal(t, "committed", got.Reports[0].Status)
	require.Equal(t, 2, got.Reports[0].Sequence)
	require.Contains(t, warningCodes(got.Diagnostics), "REPORT_ALREADY_COMMITTED")
}

func TestNormalizeRenderedContent_RejectsMissingBlockIDAndDanglingDatasource(t *testing.T) {
	missingID := NormalizeRenderedContent("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[{"kind":"dashboard.table"}]}` + "\n```")
	require.Contains(t, warningCodes(missingID.Diagnostics), "REPORT_TRANSACTION_INVALID")

	dangling := NormalizeRenderedContent("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[{"id":"table","kind":"dashboard.table","dataSourceRef":"missing"}]}` + "\n```")
	require.Contains(t, warningCodes(dangling.Diagnostics), "REPORT_TRANSACTION_INVALID")
}

func TestNormalizeRenderedContent_UsesRawDataEnvelopeForReplayEquality(t *testing.T) {
	content := "```forge-data\n" +
		`{"version":2,"reportRef":"brief","id":"rows","sequence":1,"data":[{"id":1}]}` +
		"\n```\n```forge-data\n" +
		`{"version":2,"reportRef":"brief","id":"rows","sequence":1,"data":[{"id":2}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":2,"mode":"start","blocks":[]}` + "\n```"

	got := NormalizeRenderedContent(content)
	require.Contains(t, warningCodes(got.Diagnostics), "REPORT_SEQUENCE_CONFLICT")
}

func TestNormalizeRenderedContent_EnforcesBlockAndLayoutLimits(t *testing.T) {
	blocks := make([]map[string]interface{}, 101)
	for index := range blocks {
		blocks[index] = map[string]interface{}{"id": fmt.Sprintf("block_%d", index), "kind": "markdownBlock"}
	}
	payload, err := json.Marshal(map[string]interface{}{
		"version": 1, "id": "brief", "sequence": 1, "mode": "start", "blocks": blocks,
	})
	require.NoError(t, err)
	limited := NormalizeRenderedContent("```forge-report\n" + string(payload) + "\n```")
	require.Contains(t, warningCodes(limited.Diagnostics), "REPORT_TRANSACTION_INVALID")

	layout := NormalizeRenderedContent("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[],"layout":{"items":[{"blockId":"missing"}]}}` + "\n```")
	require.Contains(t, warningCodes(layout.Diagnostics), "REPORT_TRANSACTION_INVALID")
}

func TestNormalizeRenderedContent_OrphansUnclaimedVersionTwoData(t *testing.T) {
	got := NormalizeRenderedContent("```forge-data\n" +
		`{"version":2,"reportRef":"brief","id":"rows","sequence":1,"data":[]}` +
		"\n```")

	require.Len(t, got.Reports, 1)
	require.Equal(t, "orphaned", got.Reports[0].Status)
	require.Contains(t, warningCodes(got.Diagnostics), "REPORT_DATA_ORPHANED")
}

func TestNormalizeRenderedContent_PatchesAndRemovesBlocksAtomically(t *testing.T) {
	content := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","grammar":"report-document-v1","blocks":[{"id":"first","kind":"markdownBlock","title":"Old","tags":["a","b"]},{"id":"second","kind":"calloutBlock"}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":2,"mode":"patch","blocks":[{"id":"first","title":null,"tags":["c"]}],"removeBlockIds":["second"]}` + "\n```"

	got := NormalizeRenderedContent(content)
	require.Empty(t, got.Diagnostics)
	var source struct {
		Blocks []map[string]interface{} `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal(got.Reports[0].Source, &source))
	require.Len(t, source.Blocks, 1)
	require.NotContains(t, source.Blocks[0], "title")
	require.Equal(t, []interface{}{"c"}, source.Blocks[0]["tags"])
}

func TestNormalizeRenderedContent_AppliesDatasourceReplaceAppendAndPatch(t *testing.T) {
	arrayReport := NormalizeRenderedContent("```forge-data\n" +
		`{"version":2,"reportRef":"brief","id":"rows","sequence":1,"mode":"replace","data":[{"id":1}]}` +
		"\n```\n```forge-data\n" +
		`{"version":2,"reportRef":"brief","id":"rows","sequence":2,"mode":"append","data":[{"id":2}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":3,"mode":"start","blocks":[{"id":"table","kind":"dashboard.table","dataSourceRef":"rows"}]}` + "\n```")
	require.JSONEq(t, `[{"id":1},{"id":2}]`, string(arrayReport.Reports[0].DataSources["rows"].Payload))

	objectReport := NormalizeRenderedContent("```forge-data\n" +
		`{"version":2,"reportRef":"brief","id":"summary","sequence":1,"mode":"replace","data":{"spend":1,"nested":{"a":1}}}` +
		"\n```\n```forge-data\n" +
		`{"version":2,"reportRef":"brief","id":"summary","sequence":2,"mode":"patch","data":{"spend":2,"nested":{"a":null,"b":3}}}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":3,"mode":"start","blocks":[{"id":"kpi","kind":"dashboard.summary","dataSourceRef":"summary"}]}` + "\n```")
	require.JSONEq(t, `{"spend":2,"nested":{"b":3}}`, string(objectReport.Reports[0].DataSources["summary"].Payload))
}

func TestNormalizeRenderedContent_ExplicitAndImplicitCommitRejectSequenceGaps(t *testing.T) {
	explicit := NormalizeRenderedContent("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":3,"mode":"commit"}` + "\n```")
	require.Equal(t, "incomplete", explicit.Reports[0].Status)
	require.Contains(t, warningCodes(explicit.Diagnostics), "REPORT_SEQUENCE_GAP")

	implicit := NormalizeRenderedContent("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":2,"mode":"start","blocks":[]}` + "\n```")
	require.Equal(t, "incomplete", implicit.Reports[0].Status)
	require.Contains(t, warningCodes(implicit.Diagnostics), "REPORT_SEQUENCE_GAP")
}

func TestNormalizeRenderedContent_UsesPreFragmentTargetAndReplaceReusesIDs(t *testing.T) {
	target := NormalizeRenderedContent("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","grammar":"report-document-v1","blocks":[]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":2,"mode":"append","target":{"kind":"block","ref":"new-group","slot":"childBlockIds"},"blocks":[{"id":"new-group","kind":"compositeBlock","childBlockIds":[]},{"id":"child","kind":"tableBlock"}]}` + "\n```")
	require.Contains(t, warningCodes(target.Diagnostics), "REPORT_TRANSACTION_INVALID")
	var targetSource struct {
		Blocks []json.RawMessage `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal(target.Reports[0].Source, &targetSource))
	require.Empty(t, targetSource.Blocks)

	replaced := NormalizeRenderedContent("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","grammar":"report-document-v1","blocks":[{"id":"same","kind":"markdownBlock","body":"old"}]}` +
		"\n```\n```forge-report\n" +
		`{"version":1,"id":"brief","sequence":2,"mode":"replace","grammar":"report-document-v1","blocks":[{"id":"same","kind":"markdownBlock","body":"new"}]}` + "\n```")
	require.Empty(t, replaced.Diagnostics)
	require.Contains(t, string(replaced.Reports[0].Source), `"body":"new"`)
	require.Equal(t, 1, replaced.Reports[0].ResetVersion)
}

func TestNormalizeRenderedContent_RejectsUnknownKindsAndDanglingCanonicalReferences(t *testing.T) {
	unknown := NormalizeRenderedContent("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[{"id":"mystery","kind":"dashboard.unknown"}]}` + "\n```")
	require.Contains(t, warningCodes(unknown.Diagnostics), "REPORT_TRANSACTION_INVALID")
	require.Equal(t, "mystery", unknown.Diagnostics[0].BlockID)
	require.NotEmpty(t, unknown.Diagnostics[0].SuggestedFix)

	dangling := NormalizeRenderedContent("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","grammar":"report-document-v1","blocks":[{"id":"group","kind":"compositeBlock","childBlockIds":["missing"]}]}` + "\n```")
	require.Contains(t, warningCodes(dangling.Diagnostics), "REPORT_TRANSACTION_INVALID")
}

func TestNormalizeRenderedContent_RejectsUnsafeDatasourceIdentityAndAuthoredSecrets(t *testing.T) {
	unsafeData := NormalizeRenderedContent("```forge-data\n" +
		`{"version":2,"reportRef":"brief","id":"rows/other","sequence":1,"data":[]}` + "\n```")
	require.Contains(t, warningCodes(unsafeData.Diagnostics), "REPORT_DATA_INVALID")

	secret := NormalizeRenderedContent("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","title":"Brief","metadata":{"authorization":"Bearer model-authored"},"blocks":[]}` + "\n```")
	require.Contains(t, warningCodes(secret.Diagnostics), "REPORT_TRANSACTION_INVALID")
	require.Contains(t, secret.Diagnostics[0].Message, "$.metadata.authorization")
}

func TestNormalizeRenderedContent_RejectsUnknownEnvelopeFields(t *testing.T) {
	report := NormalizeRenderedContent("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[],"surprise":true}` + "\n```")
	require.NotEmpty(t, report.Diagnostics)
	require.Equal(t, "REPORT_TRANSACTION_INVALID", report.Diagnostics[0].Code)
	require.Contains(t, report.Diagnostics[0].Message, `unknown field "surprise"`)

	data := NormalizeRenderedContent("```forge-data\n" +
		`{"version":2,"reportRef":"brief","id":"rows","sequence":1,"data":[],"query":"select *"}` + "\n```")
	require.NotEmpty(t, data.Diagnostics)
	require.Equal(t, "REPORT_DATA_INVALID", data.Diagnostics[0].Code)
	require.Contains(t, data.Diagnostics[0].Message, `unknown field "query"`)
}

func TestInlineCSVRowCount_DecodesQuotedMultilineRecords(t *testing.T) {
	rows, err := inlineCSVRowCount([]byte("name,notes\nA,\"first line\nsecond line\"\nB,ok\n"))
	require.NoError(t, err)
	require.Equal(t, 2, rows)
	_, err = inlineCSVRowCount([]byte("name,notes\nA,\"unterminated"))
	require.Error(t, err)
}

func TestValidateInlineReportWorkspaceReferences_UsesEffectiveUserAllowlist(t *testing.T) {
	content := NormalizeRenderedContent("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","title":"Brief","datasets":[{"id":"delivery","kind":"workspaceRef","dataSourceRef":"metrics_ad_cube_report"}],"blocks":[]}` + "\n```")
	require.Len(t, content.Reports, 1)
	require.Empty(t, ValidateInlineReportWorkspaceReferences(content.Reports[0], []string{"metrics_ad_cube_report"}))
	denied := ValidateInlineReportWorkspaceReferences(content.Reports[0], []string{"other_source"})
	require.Len(t, denied, 1)
	require.Equal(t, "REPORT_WORKSPACE_REF_DENIED", denied[0].Code)
	require.Equal(t, "delivery", denied[0].DataSourceID)
	require.Equal(t, "$.datasets[0].dataSourceRef", denied[0].Path)
}

func TestNormalizeRenderedContent_AcceptsEveryCanonicalReportPrimitive(t *testing.T) {
	kinds := []string{
		"markdownBlock", "filterBarBlock", "refinementBarBlock", "kpiBlock",
		"badgesBlock", "chartBlock", "tableBlock", "geoMapBlock", "sectionBlock",
		"tabGroupBlock", "compositeBlock", "stepperBlock", "infoPanelBlock",
		"calloutBlock", "kanbanBlock", "timelineBlock", "collectionBlock",
	}
	blocks := make([]map[string]interface{}, 0, len(kinds))
	for index, kind := range kinds {
		block := map[string]interface{}{"id": fmt.Sprintf("primitive_%02d", index), "kind": kind}
		if kind == "tabGroupBlock" {
			block["sectionIds"] = []string{}
		}
		if kind == "compositeBlock" {
			block["childBlockIds"] = []string{}
		}
		blocks = append(blocks, block)
	}
	payload, err := json.Marshal(map[string]interface{}{
		"version": 1, "id": "all-primitives", "sequence": 1, "mode": "start",
		"grammar": "report-document-v1", "title": "All primitives", "blocks": blocks,
	})
	require.NoError(t, err)

	content := NormalizeRenderedContent("```forge-report\n" + string(payload) + "\n```")
	require.NotNil(t, content)
	require.Empty(t, content.Diagnostics)
	require.Len(t, content.Reports, 1)
	var source struct {
		Blocks []map[string]interface{} `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal(content.Reports[0].Source, &source))
	require.Len(t, source.Blocks, len(kinds))
	for index, kind := range kinds {
		require.Equal(t, kind, source.Blocks[index]["kind"])
	}
}

func TestNormalizeRenderedContent_DepthCountsCompositionNotChartConfiguration(t *testing.T) {
	chart := map[string]interface{}{
		"id": "trend", "kind": "dashboard.timeline",
		"chart": map[string]interface{}{
			"axes": map[string]interface{}{
				"x": map[string]interface{}{
					"labels": map[string]interface{}{
						"formatter": map[string]interface{}{
							"options": map[string]interface{}{
								"locale": map[string]interface{}{"name": "en-US"},
							},
						},
					},
				},
			},
		},
	}
	payload, err := json.Marshal(map[string]interface{}{
		"version": 1, "id": "configured-chart", "sequence": 1, "mode": "start", "blocks": []interface{}{chart},
	})
	require.NoError(t, err)
	content := NormalizeRenderedContent("```forge-report\n" + string(payload) + "\n```")
	require.Empty(t, content.Diagnostics)
	require.Equal(t, "committed", content.Reports[0].Status)
}

func TestNormalizeRenderedContent_EnforcesDashboardCompositionDepthBoundary(t *testing.T) {
	build := func(depth int) map[string]interface{} {
		root := map[string]interface{}{"id": "level_1", "kind": "dashboard.summary"}
		cursor := root
		for level := 2; level <= depth; level++ {
			child := map[string]interface{}{"id": fmt.Sprintf("level_%d", level), "kind": "dashboard.summary"}
			cursor["containers"] = []interface{}{child}
			cursor = child
		}
		return root
	}
	render := func(depth int) *RenderedContent {
		payload, err := json.Marshal(map[string]interface{}{
			"version": 1, "id": fmt.Sprintf("depth-%d", depth), "sequence": 1, "mode": "start",
			"blocks": []interface{}{build(depth)},
		})
		require.NoError(t, err)
		return NormalizeRenderedContent("```forge-report\n" + string(payload) + "\n```")
	}
	require.Empty(t, render(8).Diagnostics)
	require.Contains(t, warningCodes(render(9).Diagnostics), "REPORT_TRANSACTION_INVALID")
}

func TestNormalizeRenderedContent_EnforcesCanonicalCompositionDepthAndCycles(t *testing.T) {
	build := func(depth int) []map[string]interface{} {
		blocks := make([]map[string]interface{}, 0, depth)
		for level := 1; level <= depth; level++ {
			block := map[string]interface{}{"id": fmt.Sprintf("level_%d", level), "kind": "compositeBlock", "childBlockIds": []string{}}
			if level < depth {
				block["childBlockIds"] = []string{fmt.Sprintf("level_%d", level+1)}
			}
			blocks = append(blocks, block)
		}
		return blocks
	}
	render := func(id string, blocks []map[string]interface{}) *RenderedContent {
		payload, err := json.Marshal(map[string]interface{}{
			"version": 1, "id": id, "sequence": 1, "mode": "start", "grammar": "report-document-v1", "blocks": blocks,
		})
		require.NoError(t, err)
		return NormalizeRenderedContent("```forge-report\n" + string(payload) + "\n```")
	}
	require.Empty(t, render("depth-8", build(8)).Diagnostics)
	require.Contains(t, warningCodes(render("depth-9", build(9)).Diagnostics), "REPORT_TRANSACTION_INVALID")

	cycle := []map[string]interface{}{
		{"id": "first", "kind": "compositeBlock", "childBlockIds": []string{"second"}},
		{"id": "second", "kind": "compositeBlock", "childBlockIds": []string{"first"}},
	}
	cyclic := render("cycle", cycle)
	require.Contains(t, warningCodes(cyclic.Diagnostics), "REPORT_TRANSACTION_INVALID")
	require.Contains(t, cyclic.Diagnostics[0].Message, "cycle")
}

func TestNormalizeRenderedContent_EnforcesFragmentAndDatasourceLimits(t *testing.T) {
	var fragments strings.Builder
	fragments.WriteString("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[]}` + "\n```\n")
	for sequence := 2; sequence <= 65; sequence++ {
		fragments.WriteString("```forge-report\n")
		fragments.WriteString(fmt.Sprintf(`{"version":1,"id":"brief","sequence":%d,"mode":"patch","description":"step %d"}`, sequence, sequence))
		fragments.WriteString("\n```\n")
	}
	fragmentLimited := NormalizeRenderedContent(fragments.String())
	require.Contains(t, warningCodes(fragmentLimited.Diagnostics), "REPORT_FRAGMENT_LIMIT_EXCEEDED")
	require.Equal(t, "step 64", inlineReportSourceValue(t, fragmentLimited.Reports[0], "description"))

	var datasources strings.Builder
	for sequence := 1; sequence <= 33; sequence++ {
		datasources.WriteString("```forge-data\n")
		datasources.WriteString(fmt.Sprintf(`{"version":2,"reportRef":"brief","id":"rows_%d","sequence":%d,"data":[]}`, sequence, sequence))
		datasources.WriteString("\n```\n")
	}
	datasources.WriteString("```forge-report\n" +
		`{"version":1,"id":"brief","sequence":34,"mode":"start","blocks":[]}` + "\n```\n")
	datasourceLimited := NormalizeRenderedContent(datasources.String())
	require.Contains(t, warningCodes(datasourceLimited.Diagnostics), "REPORT_DATA_LIMIT_EXCEEDED")
	require.Len(t, datasourceLimited.Reports[0].DataSources, 32)
}

func TestNormalizeRenderedContent_EnforcesStaticRowLimitBeforeMutation(t *testing.T) {
	rows := make([]int, maxInlineReportRows+1)
	payload, err := json.Marshal(map[string]interface{}{
		"version": 2, "reportRef": "brief", "id": "rows", "sequence": 1, "data": rows,
	})
	require.NoError(t, err)
	content := NormalizeRenderedContent("```forge-data\n" + string(payload) + "\n```\n" +
		"```forge-report\n" + `{"version":1,"id":"brief","sequence":2,"mode":"start","blocks":[]}` + "\n```")
	require.Contains(t, warningCodes(content.Diagnostics), "REPORT_DATA_LIMIT_EXCEEDED")
	require.Empty(t, content.Reports[0].DataSources)
}

func TestNormalizeRenderedContent_EnforcesPerReportAndAggregateStaticDataLimits(t *testing.T) {
	dataFence := func(scope, report, id string, sequence int, size int) string {
		envelope := map[string]interface{}{
			"version": 2, "scope": scope, "reportRef": report, "id": id, "sequence": sequence,
			"data": map[string]interface{}{"value": strings.Repeat("x", size)},
		}
		payload, err := json.Marshal(envelope)
		require.NoError(t, err)
		return "```forge-data\n" + string(payload) + "\n```\n"
	}
	startFence := func(scope, report string, sequence int) string {
		return "```forge-report\n" + fmt.Sprintf(
			`{"version":1,"scope":%q,"id":%q,"sequence":%d,"mode":"start","blocks":[]}`,
			scope, report, sequence,
		) + "\n```\n"
	}

	perReport := NormalizeRenderedContent(
		dataFence("scope", "brief", "first", 1, 3*1024*1024) +
			dataFence("scope", "brief", "second", 2, 3*1024*1024) +
			startFence("scope", "brief", 3),
	)
	require.Contains(t, warningCodes(perReport.Diagnostics), "REPORT_DATA_LIMIT_EXCEEDED")
	require.Contains(t, perReport.Reports[0].DataSources, "first")
	require.NotContains(t, perReport.Reports[0].DataSources, "second")

	var aggregate strings.Builder
	for index := 1; index <= 4; index++ {
		report := fmt.Sprintf("brief_%d", index)
		aggregate.WriteString(dataFence("scope", report, "rows", 1, 3*1024*1024))
		aggregate.WriteString(startFence("scope", report, 2))
	}
	aggregateLimited := NormalizeRenderedContent(aggregate.String())
	require.Contains(t, warningCodes(aggregateLimited.Diagnostics), "REPORT_DATA_LIMIT_EXCEEDED")
	require.Empty(t, aggregateLimited.Reports[3].DataSources)
}

func inlineReportSourceValue(t *testing.T, report *RenderedReportAssembly, key string) interface{} {
	t.Helper()
	var source map[string]interface{}
	require.NoError(t, json.Unmarshal(report.Source, &source))
	return source[key]
}

func warningCodes(warnings []*RenderedContentWarning) []string {
	result := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		result = append(result, warning.Code)
	}
	return result
}
