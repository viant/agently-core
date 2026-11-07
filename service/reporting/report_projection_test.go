package reporting

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	sharedregistry "github.com/viant/forge/backend/reporting/registry"
	"testing"
	"time"
)

func TestGetReportOptInSectionsPreserveCompleteRead(t *testing.T) {
	raw := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Example","blocks":[]},"reportSpec":{"title":"Example","scope":{"params":[{"id":"period"}]}},"metadata":{"large":"not requested"}}`)
	source := catalogSource{"report://example/summary": raw}
	resolver := &identity.ResourceResolver{Source: source, Policy: reportPolicyFunc(func(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
		return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "example", ValidUntil: time.Now().Add(time.Minute)}, nil
	})}
	service := New(Options{Store: NewMemoryStore(), ResourceResolver: func(context.Context, string) (*identity.ResourceResolver, error) { return resolver, nil }})
	method, err := service.Method("get_report")
	require.NoError(t, err)
	ref := &identity.ResourceRef{URI: "report://example/summary"}
	read := func(include *sharedregistry.ReportInclude) SharedArtifact {
		t.Helper()
		var result SharedArtifact
		require.NoError(t, method(context.Background(), &GetReportInput{Resource: ref, Include: include}, &result))
		return result
	}
	full := read(nil)
	require.JSONEq(t, string(raw), string(full.ResourceDefinition))
	require.NotEmpty(t, full.Document)
	selected := read(&sharedregistry.ReportInclude{Inputs: true})
	require.Len(t, selected.Sections, 1)
	require.Contains(t, selected.Sections, "inputs")
	require.Empty(t, selected.ResourceDefinition)
	require.Empty(t, selected.Document)
	require.Empty(t, selected.Metadata)
	require.Equal(t, full.Resource.URI, selected.Resource.URI)
	empty := read(&sharedregistry.ReportInclude{})
	require.Empty(t, empty.Sections)
	require.Empty(t, empty.ResourceDefinition)
	rawOnly := read(&sharedregistry.ReportInclude{Raw: true, Inputs: true})
	require.Len(t, rawOnly.Sections, 1)
	require.Contains(t, rawOnly.Sections, "raw")
	require.JSONEq(t, string(full.ResourceDefinition), string(read(nil).ResourceDefinition))
}
