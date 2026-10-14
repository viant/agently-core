package skill

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"unicode/utf8"

	proto "github.com/viant/agently-core/protocol/skill"
	format "github.com/viant/mcp-protocol/extension/skills"
	"github.com/viant/mcp-protocol/schema"
)

// ReadManifestDocument consumes the existing MCP skill manifest and an ordinary
// authenticated resource reader. Discovery transport does not change the
// manifest/file integrity contract or grant permission to execute tools.
func ReadManifestDocument(ctx context.Context, result *schema.GetSkillResult, uri, server string, maxBytes int, read func(context.Context, string) (*schema.ReadResourceResult, error)) (*proto.Skill, error) {
	if ctx == nil || ctx.Err() != nil || result == nil || result.ResultType != "complete" || result.Skill.Uri != uri || read == nil {
		return nil, fmt.Errorf("skill not available")
	}
	manifest := result.Skill
	if err := format.Validate(manifest); err != nil {
		return nil, err
	}
	if manifest.Resources.Dynamic {
		return nil, fmt.Errorf("dynamic remote skills are unsupported: content cannot be verified")
	}
	res, err := read(ctx, uri)
	if err != nil {
		return nil, err
	}
	if res == nil || len(res.Contents) != 1 {
		return nil, fmt.Errorf("skill requires exactly one text document")
	}
	content := res.Contents[0]
	if content.Uri != uri || content.Blob != "" || !utf8.ValidString(content.Text) || len(content.Text) > positive(maxBytes, 16<<20) || content.MimeType != nil && !markdownMIME(*content.MimeType) {
		return nil, fmt.Errorf("invalid skill document")
	}
	verified := false
	for _, file := range manifest.Resources.Files {
		if file.Uri == uri {
			verified = int64(len(content.Text)) == file.Size && fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content.Text))) == file.Digest
		}
	}
	front, err := format.Frontmatter([]byte(content.Text))
	if !verified || err != nil || !reflect.DeepEqual(front, manifest.Frontmatter) {
		return nil, fmt.Errorf("skill integrity verification failed")
	}
	item, diags, err := proto.Parse(uri, "", "mcp", content.Text)
	if err != nil {
		return nil, err
	}
	for _, diag := range diags {
		if diag.Level == "error" {
			return nil, fmt.Errorf("invalid skill document: %s", diag.Message)
		}
	}
	name, _ := manifest.Frontmatter["name"].(string)
	if item == nil || item.Frontmatter.Name != name {
		return nil, fmt.Errorf("skill name mismatch")
	}
	if item.Frontmatter.PreprocessEnabled() {
		return nil, fmt.Errorf("remote skill preprocessing is unsupported")
	}
	item.ServerID = server
	item.RemoteURI = uri
	item.Manifest = &manifest
	return item, nil
}
