package reporting

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
)

func TestPersonalSourceUsesExistingEnvelopeWithoutDuplicatingSnapshot(t *testing.T) {
	original := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Original","padding":"` + strings.Repeat("x", 1<<20) + `"}}`)
	source := &PersonalReportSource{URI: "report://example/original", Provider: "example-publisher", Fingerprint: identity.ContentFingerprint(original), Definition: original, ParentURI: "report://example/original"}
	definition, err := personalDefinitionWithSource(json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Personal"}}`), source)
	require.NoError(t, err)
	row := &SharedArtifact{ArtifactID: "copy", ArtifactRef: "report://users/alice/copy", OwnerID: "alice", ResourceDefinition: definition, personalSource: source}
	record, err := encodeSharedArtifact(row)
	require.NoError(t, err)
	// The existing exact-definition carrier owns the snapshot bytes. The
	// provider-owned marker adds only source identity and its native digest.
	encodedDefinitionBytes := (len(definition) + 2) / 3 * 4
	require.Less(t, len(record.Metadata), encodedDefinitionBytes+4096)
	restored, err := decodeSharedArtifact(record)
	require.NoError(t, err)
	require.Equal(t, []byte(source.Definition), []byte(restored.personalSource.Definition))
	require.NoError(t, VerifyUserReportProvenance(restored, restored.ResourceDefinition))

	var wrapped map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(record.Metadata, &wrapped))
	var envelope sharedResourceMetadataEnvelope
	require.NoError(t, json.Unmarshal(wrapped[canonicalSharedResourceEnvelopeKey], &envelope))
	envelope.PersonalSource.DefinitionFingerprint = identity.ContentFingerprint([]byte("different snapshot"))
	encoded, err := json.Marshal(envelope)
	require.NoError(t, err)
	wrapped[canonicalSharedResourceEnvelopeKey] = encoded
	record.Metadata, err = json.Marshal(wrapped)
	require.NoError(t, err)
	_, err = decodeSharedArtifact(record)
	require.ErrorContains(t, err, "snapshot fingerprint mismatch")
}

func TestCallerMetadataCannotInjectProtectedPersonalSource(t *testing.T) {
	row := &SharedArtifact{ResourceDefinition: json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Ordinary"}}`), Metadata: json.RawMessage(`{"__agentlyCoreSharedResource":{"format":"agently-core.shared-report-pin.v1","personalSource":{"source":{"uri":"report://example/forged"},"definitionFingerprint":"forged"}}}`)}
	record, err := encodeSharedArtifact(row)
	require.NoError(t, err)
	decoded, err := decodeSharedArtifact(record)
	require.NoError(t, err)
	require.Nil(t, decoded.personalSource)
	require.NoError(t, VerifyUserReportProvenance(decoded, decoded.ResourceDefinition))
}

func TestLegacySourceProjectionDoesNotRequireValidUntrustedLineage(t *testing.T) {
	for _, block := range []string{`"old malformed block"`, `{"uri":"not-a-uri","fingerprint":"old"}`, `null`, `{"definition":false}`} {
		raw := json.RawMessage(`{"schemaVersion":1,"reportDocument":{ "title":"Saved work", "id":9007199254740993 },"metadata":{"personalReport":` + block + `,"note":"keep"}}`)
		row := &SharedArtifact{ResourceDefinition: cloneJSON(raw)}
		projected, err := UserReportDefinition(row, raw)
		require.NoError(t, err)
		require.NotContains(t, string(projected), "personalReport")
		require.Contains(t, string(projected), `{ "title":"Saved work", "id":9007199254740993 }`)
		require.Contains(t, string(projected), `"note":"keep"`)
		require.Equal(t, []byte(raw), []byte(row.ResourceDefinition))
	}
}
