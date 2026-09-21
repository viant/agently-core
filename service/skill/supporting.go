package skill

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	proto "github.com/viant/agently-core/protocol/skill"
	schema "github.com/viant/mcp-protocol/schema"
	"strings"
	"time"
)

func (s *Service) readSupporting(ctx context.Context, item *proto.Skill, relative string) (string, string, string, error) {
	if strings.ContainsAny(relative, "%\\:?#") || strings.HasPrefix(relative, "/") {
		return "", "", "", fmt.Errorf("invalid skill file path")
	}
	for _, part := range strings.Split(relative, "/") {
		if part == "" || part == "." || part == ".." {
			return "", "", "", fmt.Errorf("invalid skill file path")
		}
	}
	target := strings.TrimSuffix(item.RemoteURI, "SKILL.md") + relative
	var expected *schema.SkillResource
	for i := range item.Manifest.Resources.Files {
		if item.Manifest.Resources.Files[i].Uri == target {
			expected = &item.Manifest.Resources.Files[i]
			break
		}
	}
	if expected == nil {
		return "", "", "", fmt.Errorf("file is not in held skill manifest")
	}
	cfg, err := s.mcpSource.Options(ctx, item.ServerID)
	if err != nil {
		return "", "", "", err
	}
	if cfg == nil || cfg.SkillDiscovery == nil || !cfg.SkillDiscovery.Enabled {
		return "", "", "", fmt.Errorf("skill not available")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(positive(cfg.SkillDiscovery.TimeoutSec, 10))*time.Second)
	defer cancel()
	ctx, cli, opts, err := s.remoteClient(ctx, item.ServerID, cfg)
	if err != nil {
		return "", "", "", err
	}
	result, err := cli.ReadResource(ctx, &schema.ReadResourceRequestParams{Uri: target}, opts...)
	if err != nil || result == nil || len(result.Contents) != 1 {
		return "", "", "", fmt.Errorf("skill file unavailable")
	}
	c := result.Contents[0]
	data := []byte(c.Text)
	if c.Blob != "" {
		if c.Text != "" {
			return "", "", "", fmt.Errorf("ambiguous skill content")
		}
		data, err = base64.StdEncoding.DecodeString(c.Blob)
	}
	if err != nil || c.Uri != target || int64(len(data)) != expected.Size || fmt.Sprintf("sha256:%x", sha256.Sum256(data)) != expected.Digest {
		return "", "", "", fmt.Errorf("skill file verification failed")
	}
	mime := ""
	if c.MimeType != nil {
		mime = *c.MimeType
	}
	return c.Text, c.Blob, mime, nil
}
