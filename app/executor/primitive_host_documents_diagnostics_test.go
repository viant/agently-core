package executor

import (
	"bytes"
	"context"
	"log"
	"testing"

	"github.com/stretchr/testify/require"
	producer "github.com/viant/forge/backend/resourcehost/producer"
	"github.com/viant/mcp-protocol/schema"
)

func TestNativeHostDocumentFailureLogIdentifiesRegisteredFile(t *testing.T) {
	t.Setenv("AGENTLY_DEBUG", "off")
	t.Setenv("AGENTLY_DEBUG_COMPONENTS", "other")
	docs := []producer.NativeResource{
		{Metadata: schema.Resource{Uri: "agently-runtime://workspace/example"}},
		{Metadata: schema.Resource{Uri: "agently-runtime://other/example"}},
	}
	attachNativeHostDocumentFailureDiagnostics(docs, map[string]string{
		"tools/bundles/example.yaml": docs[0].Metadata.Uri,
	})
	require.NotNil(t, docs[0].OnFailure)
	require.Nil(t, docs[1].OnFailure)
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	docs[0].OnFailure(context.Background(), producer.NativeResourceFailure{Stage: "current_actor", Category: "denied"})
	logs := output.String()
	require.Contains(t, logs, `file="tools/bundles/example.yaml" stage="current_actor" category="denied"`)
	require.NotContains(t, logs, docs[0].Metadata.Uri)
}
