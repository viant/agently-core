package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
	"github.com/viant/forge/backend/reporting/materializer"
	"github.com/viant/forge/backend/reporting/registry"
	"github.com/viant/jsonrpc"
	"os"
	"testing"
)

func TestPersonalCopyPreservesFingerprintBearingDefinitionBytes(t *testing.T) {
	original, err := os.ReadFile("testdata/synthetic_authored_report.json")
	require.NoError(t, err)
	var envelope registry.ReportEnvelope
	require.NoError(t, json.Unmarshal(original, &envelope))
	var builder bytes.Buffer
	require.NoError(t, json.Indent(&builder, envelope.BuilderDefinition, "", "  "))
	original = bytes.Replace(original, envelope.BuilderDefinition, builder.Bytes(), 1)
	original = bytes.ReplaceAll(original, []byte(identity.ContentFingerprint(envelope.BuilderDefinition)), []byte(identity.ContentFingerprint(builder.Bytes())))
	_, err = materializer.LowerForValidation(context.Background(), original)
	require.NoError(t, err, "the source is a valid authored report with exact byte fingerprints")
	pin := &identity.ResolvedResource{URI: "report://example/source"}
	pin.ContentFingerprint = identity.ContentFingerprint(original)
	prepared, err := preparePersonalDefinition(original, nil, pin)
	require.NoError(t, err)
	_, err = materializer.LowerForValidation(context.Background(), prepared)
	require.NoError(t, err, "copy preparation must preserve fingerprint-bearing authored subtrees")
}

func TestPersonalReportLineageRetainsRootWithoutMaterializedResults(t *testing.T) {
	original := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Original"},"reportFill":{"secretRows":[1]},"reportPrint":{},"compileState":{},"metadata":{"publisher":"example"},"dataSourceResources":{"rows":{"uri":"datasource://example/rows"}}}`)
	pin := &identity.ResolvedResource{URI: "report://example/overview"}
	pin.ContentFingerprint = identity.ContentFingerprint(original)
	copy, err := preparePersonalDefinition(original, nil, pin)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(copy, &fields))
	require.NotContains(t, fields, "reportFill")
	require.NotContains(t, fields, "reportPrint")
	require.NotContains(t, fields, "compileState")
	source, err := personalReportSource(copy)
	require.NoError(t, err)
	require.Equal(t, pin.URI, source.URI)
	require.NotContains(t, string(source.Definition), "secretRows")
	child := &identity.ResolvedResource{URI: "report://users/alice/personal"}
	child.ContentFingerprint = identity.ContentFingerprint(copy)
	nested, err := preparePersonalDefinition(copy, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Nested"}}`), child)
	require.NoError(t, err)
	nestedSource, err := personalReportSource(nested)
	require.NoError(t, err)
	require.Equal(t, pin.URI, nestedSource.URI)
	require.Equal(t, child.URI, nestedSource.ParentURI)
	require.JSONEq(t, string(source.Definition), string(nestedSource.Definition))
	edited, err := preservePersonalLineage(nested, json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Edited"},"metadata":{"personalReport":{"uri":"report://forged/source"}}}`))
	require.NoError(t, err)
	editedSource, err := personalReportSource(edited)
	require.NoError(t, err)
	require.Equal(t, pin.URI, editedSource.URI)
}

func TestPersonalReportMutationReceiptExcludesDefinitionAndSourceSnapshot(t *testing.T) {
	original := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Original"}}`)
	pin := &identity.ResolvedResource{URI: "report://example/overview", ProviderIdentity: "example"}
	pin.ContentFingerprint = identity.ContentFingerprint(original)
	definition, err := preparePersonalDefinition(original, nil, pin)
	require.NoError(t, err)
	report := &SharedArtifact{ArtifactRef: "report://users/example/weekly", Title: "Weekly", ResourceDefinition: definition, Document: json.RawMessage(`{"title":"Weekly"}`), ReportFill: json.RawMessage(`{"rows":[{"amount":7}]}`)}
	receipt := personalReportReceipt(report)
	require.Equal(t, report.ArtifactRef, receipt.ArtifactRef)
	require.Empty(t, receipt.ResourceDefinition)
	require.Empty(t, receipt.Document)
	require.Empty(t, receipt.ReportFill)
	var metadata map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(receipt.Metadata, &metadata))
	require.JSONEq(t, `"report://example/overview"`, string(metadata["personalReport"]["uri"]))
	require.NotContains(t, metadata["personalReport"], "definition")
	require.NotEmpty(t, report.ResourceDefinition, "receipt does not mutate the persisted source")
}

func TestExplicitReportRevisionNeverUsesPersonalDefaults(t *testing.T) {
	reads := 0
	service := &Service{personalWorkspacePreferences: func(context.Context) (*authsvc.WorkspacePreferences, error) {
		reads++
		return &authsvc.WorkspacePreferences{}, nil
	}}
	report, selected, err := service.preferredPersonalReport(context.Background(), &GetReportInput{PreferPersonal: true, Resource: &identity.ResourceRef{URI: "report://example/overview", Revision: "7"}})
	require.NoError(t, err)
	require.False(t, selected)
	require.Nil(t, report)
	require.Zero(t, reads, "exact revision requests add no preference I/O")
}

func TestPersonalReportRootBindingRetainsOtherDependencies(t *testing.T) {
	raw := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Copy"},"dependencies":[{"kind":"report","id":"report://example/original","contentFingerprint":"old","unexpected":"retained-for-validator"},{"kind":"report","id":"report://example/linked","contentFingerprint":"linked-fingerprint"},{"kind":"datasource","id":"rows","contentFingerprint":"datasource-fingerprint"}]}`)
	bound, err := bindPersonalReportRoot(raw, "report://example/original", "report://users/example/copy")
	require.NoError(t, err)
	require.Contains(t, string(bound), `"unexpected":"retained-for-validator"`, "rewriting the self reference cannot discard fields before native validation")
	var envelope registry.ReportEnvelope
	require.NoError(t, json.Unmarshal(bound, &envelope))
	require.Equal(t, "report://users/example/copy", envelope.Dependencies[0].ID)
	require.Equal(t, identity.ContentFingerprint(envelope.ReportDocument), envelope.Dependencies[0].ContentFingerprint)
	require.Equal(t, "report://example/linked", envelope.Dependencies[1].ID)
	require.Equal(t, "linked-fingerprint", envelope.Dependencies[1].ContentFingerprint)
	require.Equal(t, "datasource-fingerprint", envelope.Dependencies[2].ContentFingerprint)
}

func TestPersonalReportTitleMatchesDocumentAndSpecWithoutChangingSource(t *testing.T) {
	original := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Original","value":9007199254740993},"reportSpec":{"title":"Original","datasets":[]}}`)
	prepared, err := preparePersonalDefinition(original, nil, &identity.ResolvedResource{URI: "report://example/source"})
	require.NoError(t, err)
	personal, err := setPersonalReportTitle(prepared, "My weekly review")
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(personal, &fields))
	require.Contains(t, string(fields["reportDocument"]), `"title":"My weekly review"`)
	require.Contains(t, string(fields["reportDocument"]), `9007199254740993`)
	require.Contains(t, string(fields["reportSpec"]), `"title":"My weekly review"`)
	source, err := personalReportSource(personal)
	require.NoError(t, err)
	require.JSONEq(t, string(original), string(source.Definition))
	reset, err := retainPersonalReportTitle(personal, source.Definition)
	require.NoError(t, err)
	require.Contains(t, string(reset), `"title":"My weekly review"`)
	specOnly, err := retainPersonalReportTitle(json.RawMessage(`{"schemaVersion":1,"reportSpec":{"title":"My spec"}}`), original)
	require.NoError(t, err)
	require.Contains(t, string(specOnly), `"title":"My spec"`)
}

func TestPersonalReportRootBindingPreservesUnrelatedDependencyBytes(t *testing.T) {
	raw := json.RawMessage(`{
 "schemaVersion":1,"reportDocument":{ "title": "<Report & unchanged>" },
 "dependencies":[
  { "kind": "datasource", "id": "datasource://example/rows", "custom": { "text": "<data & unchanged>" } },
  { "kind": "report", "id": "report://example/source", "contentFingerprint":"old", "custom": { "text": "<self & unchanged>" } }
 ]}`)
	var before struct {
		Dependencies []json.RawMessage `json:"dependencies"`
		Document     json.RawMessage   `json:"reportDocument"`
	}
	require.NoError(t, json.Unmarshal(raw, &before))
	unchanged, err := bindPersonalReportRoot(raw, "report://example/other", "report://users/alice/copy")
	require.NoError(t, err)
	require.Equal(t, raw, unchanged, "no matching self dependency means no serialization")
	bound, err := bindPersonalReportRoot(raw, "report://example/source", "report://users/alice/copy")
	require.NoError(t, err)
	var after struct {
		Dependencies []json.RawMessage `json:"dependencies"`
		Document     json.RawMessage   `json:"reportDocument"`
	}
	require.NoError(t, json.Unmarshal(bound, &after))
	require.Equal(t, before.Dependencies[0], after.Dependencies[0])
	require.Equal(t, before.Document, after.Document)
	require.Contains(t, string(after.Dependencies[1]), `"custom": { "text": "<self & unchanged>" }`)
	var self struct {
		ID          string `json:"id"`
		Fingerprint string `json:"contentFingerprint"`
	}
	require.NoError(t, json.Unmarshal(after.Dependencies[1], &self))
	require.Equal(t, "report://users/alice/copy", self.ID)
	require.Equal(t, identity.ContentFingerprint(before.Document), self.Fingerprint)
}

func TestPersonalReportRootBindingFingerprintsReportSpec(t *testing.T) {
	raw := json.RawMessage(`{"schemaVersion":1,"reportSpec":{ "title":"Example","blocks":[] },"dependencies":[{"kind":"report","id":"report://example/source"}]}`)
	result, err := bindPersonalReportRoot(raw, "report://example/source", "report://users/alice/copy")
	require.NoError(t, err)
	var document struct {
		Spec         json.RawMessage `json:"reportSpec"`
		Dependencies []struct {
			Fingerprint string `json:"contentFingerprint"`
		} `json:"dependencies"`
	}
	require.NoError(t, json.Unmarshal(result, &document))
	require.Equal(t, identity.ContentFingerprint(document.Spec), document.Dependencies[0].Fingerprint)
	require.NotEqual(t, identity.ContentFingerprint(nil), document.Dependencies[0].Fingerprint)
}

func TestPersonalCopyUsesActualSharedSourceInsteadOfPublisherProvenance(t *testing.T) {
	for _, metadata := range []string{`{"uri":"report://example/forged","provider":"forged","definition":{"reportDocument":{"title":"Forged"}}}`, `"malformed publisher block"`} {
		original := json.RawMessage(`{"schemaVersion":1,"reportDocument":{ "title":"Shared source" },"metadata":{"personalReport":` + metadata + `,"publisher":"example"}}`)
		pin := &identity.ResolvedResource{URI: "report://example/source", ProviderIdentity: "publisher"}
		pin.ContentFingerprint = identity.ContentFingerprint(original)
		definition, err := preparePersonalDefinition(original, nil, pin)
		require.NoError(t, err)
		lineage, err := personalReportSource(definition)
		require.NoError(t, err)
		require.Equal(t, pin.URI, lineage.URI)
		require.Equal(t, pin.ProviderIdentity, lineage.Provider)
		require.Equal(t, pin.ContentFingerprint, lineage.Fingerprint)
		require.NotContains(t, string(lineage.Definition), "personalReport")
		require.Contains(t, string(lineage.Definition), `"reportDocument":{ "title":"Shared source" }`)
	}
}

func TestPersonalEditCannotIntroduceLineageOnAnUnrelatedReport(t *testing.T) {
	original := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Original"}}`)
	candidate := json.RawMessage(`{"schemaVersion":1,"reportDocument":{ "title":"Edited <value>" },"metadata":{"personalReport":{"uri":"report://example/forged"},"other":"keep"}}`)
	result, err := preservePersonalLineage(original, candidate)
	require.NoError(t, err)
	source, err := personalReportSource(result)
	require.NoError(t, err)
	require.Nil(t, source)
	require.Contains(t, string(result), `"reportDocument":{ "title":"Edited <value>" }`)
	require.Contains(t, string(result), `"other":"keep"`)
}

func TestPersonalDefaultRejectsNonUserTargetsBeforeResourceReads(t *testing.T) {
	service := &Service{}
	entry := authsvc.WorkspaceReportDefault{SourceURI: "report://example/source", SourceProvider: "publisher", TargetURI: "report://example/forged"}
	require.ErrorContains(t, service.ValidatePersonalDefault(context.Background(), entry), "target must be a user report")
	service.SetPersonalWorkspacePreferences(func(context.Context) (*authsvc.WorkspacePreferences, error) {
		return &authsvc.WorkspacePreferences{Defaults: []authsvc.WorkspaceReportDefault{entry}}, nil
	})
	_, selected, err := service.preferredPersonalReport(context.Background(), &GetReportInput{Resource: &identity.ResourceRef{URI: entry.SourceURI}, PreferPersonal: true})
	require.True(t, selected)
	require.ErrorContains(t, err, "target must be a user report")
}

func TestPersonalProvenanceGuardsRejectAmbiguousJSONKeys(t *testing.T) {
	shapes := []json.RawMessage{
		json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Example"},"metadata":{"personalRepor\u0074":{"uri":"report://example/forged"}}}`),
		json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Example"},"metadata":{"PersonalReport":"wrong type"}}`),
		json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Example"},"metadata":{"PersonalReport":{"uri":"report://example/forged"}}}`),
		json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Example"},"metadata":{"personalReport":{},"PersonalReport":{"uri":"report://example/forged"}}}`),
		json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Example"},"metadata":{"personalReport":{},"personalReport":{"uri":"report://example/forged"}}}`),
		json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Example"},"metadata":{},"Metadata":{"personalReport":{"uri":"report://example/forged"}}}`),
		json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Example"},"metadata":{"personalReport":{}},"metadata":{"personalReport":{"uri":"report://example/forged"}}}`),
	}
	original := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Original"}}`)
	source := &PersonalReportSource{URI: "report://example/source", Provider: "publisher", Definition: json.RawMessage("  { \"title\": \"Original\" }  ")}
	for index, candidate := range shapes {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			cleared, err := preservePersonalLineage(original, candidate)
			if err == nil {
				decoded, readErr := personalReportSource(cleared)
				require.NoError(t, readErr)
				require.Nil(t, decoded)
			} else {
				require.ErrorIs(t, err, identity.ErrResource)
			}
			assigned, err := personalDefinitionWithSource(candidate, source)
			if err == nil {
				decoded, readErr := personalReportSource(assigned)
				require.NoError(t, readErr)
				require.True(t, samePersonalReportSource(source, decoded))
			} else {
				require.ErrorIs(t, err, identity.ErrResource)
			}
			copied, err := preparePersonalDefinition(candidate, nil, &identity.ResolvedResource{URI: "report://example/source", ProviderIdentity: "publisher"})
			if err == nil {
				decoded, readErr := personalReportSource(copied)
				require.NoError(t, readErr)
				require.Equal(t, "report://example/source", decoded.URI)
				require.Equal(t, "publisher", decoded.Provider)
			} else {
				require.ErrorIs(t, err, identity.ErrResource)
			}
		})
	}
	valid, err := personalDefinitionWithSource(original, source)
	require.NoError(t, err, "ordinary authored JSON keeps its exact snapshot bytes")
	decoded, err := personalReportSource(valid)
	require.NoError(t, err)
	require.True(t, samePersonalReportSource(source, decoded))
}

func TestPersonalProvenanceDecodeDistinguishesStoredStateFromWriteValidation(t *testing.T) {
	raw := json.RawMessage(`{"metadata":{"PersonalReport":"wrong type"}}`)
	_, err := personalReportSource(raw)
	require.NotErrorIs(t, err, identity.ErrResource, "stored bytes must not be attributed to caller input")
	var decode *json.UnmarshalTypeError
	require.ErrorAs(t, err, &decode)
	_, err = UserReportDefinition(&SharedArtifact{}, raw)
	require.NotErrorIs(t, err, identity.ErrResource, "stored projection failures must not become HTTP 400")
	require.ErrorAs(t, err, &decode, "keep the cause available for diagnosis")
	_, err = clearPersonalReportSource(raw)
	require.ErrorIs(t, err, identity.ErrResource, "a rejected write candidate is validation")
	require.ErrorAs(t, err, &decode)
}

func TestUnqualifiedReportWithTwoDefaultSourcesRequiresAChoice(t *testing.T) {
	const uri = "report://example/summary"
	preferences := &authsvc.WorkspacePreferences{Defaults: []authsvc.WorkspaceReportDefault{
		{SourceProvider: "first", SourceURI: uri, TargetURI: "report://users/owner/first"},
		{SourceProvider: "second", SourceURI: uri, TargetURI: "report://users/owner/second"},
	}}
	service := &Service{personalWorkspacePreferences: func(context.Context) (*authsvc.WorkspacePreferences, error) { return preferences, nil }}
	report, handled, err := service.preferredPersonalReport(context.Background(), &GetReportInput{ArtifactRef: uri, PreferPersonal: true})
	require.True(t, handled)
	require.Nil(t, report)
	require.ErrorContains(t, err, "choose a personal report or open the original explicitly")
}

func TestDefaultReportStoreOutageDoesNotRecommendChangingDefault(t *testing.T) {
	entry := authsvc.WorkspaceReportDefault{SourceURI: "report://example/source", SourceProvider: "example", TargetURI: "report://users/example/personal"}
	s := &Service{}
	s.SetPersonalWorkspacePreferences(func(context.Context) (*authsvc.WorkspacePreferences, error) {
		return &authsvc.WorkspacePreferences{Defaults: []authsvc.WorkspaceReportDefault{entry}}, nil
	})
	cause := jsonrpc.NewHTTPStatusError(503)
	s.SetResourceReaderFactory(func(context.Context, string) (ResourceReader, error) { return nil, cause })
	_, handled, err := s.preferredPersonalReport(t.Context(), &GetReportInput{Resource: &identity.ResourceRef{URI: entry.SourceURI}, PreferPersonal: true})
	require.True(t, handled)
	require.ErrorIs(t, err, cause)
	require.NotErrorIs(t, err, ErrPersonalDefaultUnavailable)
}
