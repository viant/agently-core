package resource

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	identity "github.com/viant/agently-core/protocol/resource"
)

func extensionWrite(t *testing.T, root, name, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(data), 0600))
}
func extensionJSON(ctx context.Context, reader *ExtensionReader, file string) (json.RawMessage, error) {
	raw, err := reader.ReadFile(ctx, file)
	return json.RawMessage(raw), err
}
func extensionPolicy(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
	return localFixturePolicy{actor: a}, nil
}
