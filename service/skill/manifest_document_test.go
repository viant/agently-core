package skill

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	format "github.com/viant/mcp-protocol/extension/skills"
	"github.com/viant/mcp-protocol/schema"
	"testing"
	"testing/fstest"
)

func TestManifestDocumentUsesExactExistingMCPContract(t *testing.T) {
	const uri = "skill://team/review/SKILL.md"
	const body = "---\nname: review\ndescription: Original description\n---\nOriginal instruction."
	compiled, err := (format.Compiler{Source: fstest.MapFS{"SKILL.md": {Data: []byte(body)}}}).Compile(context.Background(), uri)
	require.NoError(t, err)
	for _, scenario := range []string{"accepted", "owner-denied", "wrong-uri", "missing-file", "changed-document", "changed-frontmatter", "dynamic"} {
		t.Run(scenario, func(t *testing.T) {
			result := &schema.GetSkillResult{ResultType: "complete", Skill: compiled.Metadata()}
			calls := 0
			owningError := errors.New("owning MCP unavailable")
			reader := func(ctx context.Context, want string) (*schema.ReadResourceResult, error) {
				calls++
				require.Equal(t, uri, want)
				if scenario == "owner-denied" {
					return nil, owningError
				}
				read, e := compiled.ReadResource(ctx, &schema.ReadResourceRequest{Params: schema.ReadResourceRequestParams{Uri: want}})
				require.Nil(t, e)
				if scenario == "changed-document" {
					read.Contents[0].Text += "changed"
				}
				if scenario == "changed-frontmatter" {
					read.Contents[0].Text = "---\nname: another\ndescription: Another\n---\nchanged"
				}
				return read, nil
			}
			switch scenario {
			case "wrong-uri":
				result.Skill.Uri = "skill://other/review/SKILL.md"
			case "missing-file":
				result.Skill.Resources.Files = nil
			case "dynamic":
				result.Skill.Resources.Files = nil
				result.Skill.Resources.Dynamic = true
			}
			item, err := ReadManifestDocument(context.Background(), result, uri, "actual-connection", 0, reader)
			if scenario != "accepted" {
				require.Error(t, err)
				require.Nil(t, item)
				if scenario == "owner-denied" {
					require.ErrorIs(t, err, owningError)
					require.Equal(t, 1, calls)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, "review", item.Frontmatter.Name)
			require.Equal(t, uri, item.RemoteURI)
			require.Equal(t, "actual-connection", item.ServerID)
			require.Empty(t, item.Root)
			require.NotNil(t, item.Manifest)
			require.Equal(t, 1, calls)
		})
	}
}
