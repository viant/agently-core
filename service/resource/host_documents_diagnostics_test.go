package resource

import (
	"bytes"
	"context"
	"errors"
	"log"
	"testing"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
)

func TestDeniedHostDocumentReadIdentifiesResourceWithoutCredentials(t *testing.T) {
	t.Setenv("AGENTLY_DEBUG", "off")
	t.Setenv("AGENTLY_DEBUG_COMPONENTS", "other")
	denied := errors.New("private-error-message")
	gateway := NewGateway(nil, func(context.Context) (identity.VerifiedActor, error) {
		return identity.VerifiedActor{}, denied
	}, nil, "synthetic")
	documents := &HostDocuments{Gateway: gateway, Connection: "internal", Provider: "synthetic", Files: map[string]string{
		"tools/bundles/example.yaml": "agently-runtime://workspace/example?token=private-token",
	}}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	_, err := documents.Read(context.Background(), "tools/bundles/example.yaml")
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	logs := output.String()
	require.Contains(t, logs, `file="tools/bundles/example.yaml"`)
	require.Contains(t, logs, `uri="<redacted>"`)
	require.Contains(t, logs, `connection="internal" provider="synthetic"`)
	require.NotContains(t, logs, "private-token")
	require.NotContains(t, logs, "private-error-message")
}
