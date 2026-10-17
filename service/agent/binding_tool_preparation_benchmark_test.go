package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/runtime/toolselection"
	"github.com/viant/agently-core/service/intake"
	bundlerepo "github.com/viant/agently-core/workspace/repository/toolbundle"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

// Synthetic local authoring/binding work only: no remote registry or model.
// The same source runs against the prior implementation through a Go overlay.
func BenchmarkBindingBundlePreparation(b *testing.B) {
	b.Setenv("AGENTLY_DEBUG_TRACE_FILE", "")
	root := b.TempDir()
	dir := filepath.Join(root, "tools", "bundles")
	require.NoError(b, os.MkdirAll(dir, 0755))
	require.NoError(b, os.WriteFile(filepath.Join(dir, "base.yaml"), []byte("id: base\nmatch:\n  - name: 'catalog:*'\n"), 0644))
	require.NoError(b, os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte("id: workflow\nmatch:\n  - name: 'catalog:write'\n"), 0644))
	for i := 0; i < 70; i++ {
		source := fmt.Sprintf("id: other-%03d\ndescription: Another authored workflow using the same connected catalog.\nmatch:\n  - name: 'catalog:read'\n", i)
		require.NoError(b, os.WriteFile(filepath.Join(dir, fmt.Sprintf("other-%03d.yaml", i)), []byte(source), 0644))
	}
	s := selectionService()
	preparationConversation(b, s)
	repo := bundlerepo.NewWithStore(fsstore.New(root))
	reads := 0
	s.toolBundles = func(ctx context.Context) ([]*toolbundle.Bundle, error) { reads++; return repo.LoadAll(ctx) }
	input := selectionInput(&intake.ToolSelection{Names: []string{"catalog:read"}, Complete: true})
	input.ConversationID, input.MessageID = "conversation", "turn"
	input.ToolBundles = []string{"workflow"}
	ctx := toolselection.WithState(context.Background(), nil)
	warm, err := s.BuildBinding(ctx, input)
	require.NoError(b, err)
	require.ElementsMatch(b, []string{"catalog-read", "catalog-write"}, signatureNames(warm.Tools.Signatures))
	reads = 0
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		binding, err := s.BuildBinding(ctx, input)
		if err != nil {
			b.Fatal(err)
		}
		if len(binding.Tools.Signatures) != 2 {
			b.Fatalf("unexpected schema count: %d", len(binding.Tools.Signatures))
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(reads)/float64(b.N), "inventories/op")
	b.ReportMetric(2, "schemas")
}
