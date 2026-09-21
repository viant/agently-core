package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	authctx "github.com/viant/agently-core/internal/auth"
	format "github.com/viant/mcp-protocol/extension/skills"
	schema "github.com/viant/mcp-protocol/schema"
	"strconv"
	"strings"
)

func (a *runtimeExecutorAdapter) localSkills(ctx context.Context) ([]*format.Static, error) {
	if len(a.skillItems) == 0 {
		return nil, nil
	}
	if authctx.EffectiveUserID(ctx) == "" || a.rt == nil || a.rt.Skills == nil {
		return nil, fmt.Errorf("skill unavailable")
	}
	return a.rt.Skills.ExportLocal(ctx, a.skillItems)
}
func (a *runtimeExecutorAdapter) ListSkills(ctx context.Context, cursor *string) (*schema.ListSkillsResult, error) {
	skills, err := a.localSkills(ctx)
	if err != nil {
		return nil, err
	}
	entries := []schema.Skill{}
	for _, s := range skills {
		entries = append(entries, s.Metadata())
	}
	encoded, _ := json.Marshal(entries)
	revision := fmt.Sprintf("%x", sha256.Sum256(encoded))
	start := 0
	if cursor != nil {
		raw, err := base64.RawURLEncoding.DecodeString(*cursor)
		parts := strings.Split(string(raw), ":")
		if err != nil || len(parts) != 2 || parts[0] != revision {
			return nil, fmt.Errorf("invalid cursor")
		}
		start, err = strconv.Atoi(parts[1])
		if err != nil || start < 0 || start >= len(entries) {
			return nil, fmt.Errorf("invalid cursor")
		}
	}
	end := min(start+32, len(entries))
	ttl := 0
	out := &schema.ListSkillsResult{ResultType: "complete", Skills: entries[start:end], TtlMs: &ttl, CacheScope: "private"}
	if end < len(entries) {
		next := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%d", revision, end)))
		out.NextCursor = &next
	}
	return out, nil
}
func (a *runtimeExecutorAdapter) GetSkill(ctx context.Context, uri string) (*schema.GetSkillResult, error) {
	skills, err := a.localSkills(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range skills {
		entry := s.Metadata()
		if entry.Uri == uri {
			ttl := 0
			return &schema.GetSkillResult{ResultType: "complete", Skill: entry, TtlMs: &ttl, CacheScope: "private"}, nil
		}
	}
	return nil, fmt.Errorf("skill unavailable")
}
