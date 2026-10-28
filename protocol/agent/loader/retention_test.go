package loader

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	meta "github.com/viant/agently-core/workspace/service/meta"
)

func TestAgentSkillRetentionSelectorsLoadFromNativeFormats(t *testing.T) {
	for _, format := range []string{"yaml", "md"} {
		for _, selectors := range []string{"review", "[review, audit, review]"} {
			t.Run(format+selectors, func(t *testing.T) {
				root := t.TempDir()
				require.NoError(t, os.Mkdir(filepath.Join(root, "agents"), 0700))
				raw := "id: reviewer\nskills: [review]\nskillsAlwaysInclude: " + selectors + "\n"
				if format == "md" {
					raw = "---\n" + raw + "---\nReview synthetic records.\n"
				}
				require.NoError(t, os.WriteFile(filepath.Join(root, "agents", "reviewer."+format), []byte(raw), 0600))
				loader := New(WithMetaService(meta.New(afs.New(), root)))
				loaded, err := loader.Load(context.Background(), "reviewer")
				require.NoError(t, err)
				expected := []string{"review"}
				if selectors != "review" {
					expected = []string{"audit", "review"}
				}
				require.Equal(t, expected, loaded.SkillsAlwaysInclude)
				require.Equal(t, []string{"review"}, loaded.Skills, "retention never expands visibility")
				document, err := DecodeDocument(context.Background(), "agent://example/reviewer", []byte(raw))
				require.NoError(t, err)
				require.Equal(t, expected, document.SkillsAlwaysInclude)
			})
		}
	}
}
func TestAgentSkillRetentionRejectsInvalidShape(t *testing.T) {
	_, err := DecodeDocument(context.Background(), "agent://example/reviewer", []byte("id: reviewer\nskillsAlwaysInclude: {review: true}\n"))
	require.ErrorContains(t, err, "skillsAlwaysInclude")
}
