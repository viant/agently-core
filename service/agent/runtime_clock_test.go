package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/binding"
)

func TestAppendRuntimeClockSystemDocument(t *testing.T) {
	b := &binding.Binding{}
	now := time.Date(2026, 5, 4, 17, 30, 0, 0, time.FixedZone("PDT", -7*60*60))

	appendRuntimeClockSystemDocument(b, now)
	require.Len(t, b.SystemDocuments.Items, 1)
	doc := b.SystemDocuments.Items[0]
	require.Equal(t, runtimeClockSourceURI, doc.SourceURI)
	require.Contains(t, doc.PageContent, "current_date: 2026-05-04")
	require.Contains(t, doc.PageContent, "previous_date: 2026-05-03")
	require.Contains(t, doc.PageContent, "timezone: PDT")

	appendRuntimeClockSystemDocument(b, now)
	require.Len(t, b.SystemDocuments.Items, 1)
}
