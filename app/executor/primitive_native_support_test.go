package executor

import (
	"context"
	"github.com/stretchr/testify/require"
	proto "github.com/viant/agently-core/protocol/skill"
	"strings"
	"testing"
)

func TestNativeSkillSupportingReferenceDoesNotEnterGenericCatalog(t *testing.T) {
	for _, revision := range []string{"", strings.Repeat("a", 64)} {
		t.Run(revision, func(t *testing.T) {
			ref := proto.Ref{ServerID: "publisher", Name: "review", ResourceURI: "skill://example/review/SKILL.md", Revision: revision}
			item := &proto.Skill{CatalogURI: ref.URI(), RemoteURI: ref.ResourceURI, ServerID: "source", Source: "mcp"}
			// No generic gateway is installed: a native reference must not scan it.
			result, handled, err := (&primitiveSkillDelivery{}).ReadRemoteResource(context.Background(), item, "skill://example/review/refs/note.txt")
			require.NoError(t, err)
			require.False(t, handled)
			require.Nil(t, result)
		})
	}
}
