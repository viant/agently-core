package executionprotection

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	execconfig "github.com/viant/agently-core/app/executor/config"
	toolprotection "github.com/viant/agently-core/protocol/tool/protection"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

const claimKeyVersion = "agently.tool-execution-claim.v1"

type Repository interface {
	Claim(ctx context.Context, record ClaimRecord) (bool, error)
	Finish(ctx context.Context, claimKey string, state toolprotection.State, finishedAt time.Time) error
}

type ClaimRecord struct {
	ClaimKey          string
	RuleID            string
	CanonicalToolName string
	TurnID            string
	SemanticHash      string
	CreatedAt         time.Time
}

type Service struct {
	rules      map[string]compiledRule
	repository Repository
}

type compiledRule struct {
	id       string
	tool     string
	fields   map[string]struct{}
	excludes map[string]struct{}
}

func New(cfg execconfig.ToolExecutionProtectionDefaults, repository Repository) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	service := &Service{rules: map[string]compiledRule{}, repository: repository}
	if !cfg.Enabled {
		return service, nil
	}
	for _, source := range cfg.Rules {
		toolName, _ := execconfig.CanonicalProtectedToolName(source.Tool)
		rule := compiledRule{id: strings.TrimSpace(source.ID), tool: toolName, excludes: map[string]struct{}{}}
		if semantic := source.SemanticArguments; semantic != nil {
			if semantic.Fields != nil && !(len(semantic.Fields) == 1 && semantic.Fields[0] == "*") {
				rule.fields = make(map[string]struct{}, len(semantic.Fields))
				for _, field := range semantic.Fields {
					rule.fields[field] = struct{}{}
				}
			}
			for _, field := range semantic.ExcludeFields {
				rule.excludes[field] = struct{}{}
			}
		}
		service.rules[toolName] = rule
	}
	return service, nil
}

func (s *Service) IsProtected(name string) bool {
	if s == nil || len(s.rules) == 0 {
		return false
	}
	canonical, ok := canonicalRuntimeToolName(name)
	if !ok {
		return false
	}
	_, ok = s.rules[canonical]
	return ok
}

func (s *Service) Claim(ctx context.Context, name string, args map[string]interface{}) (toolprotection.Claim, error) {
	canonical, ok := canonicalRuntimeToolName(name)
	if !ok || s == nil {
		return toolprotection.Claim{}, nil
	}
	rule, ok := s.rules[canonical]
	if !ok {
		return toolprotection.Claim{}, nil
	}
	decision := toolprotection.Claim{Protected: true, RuleID: rule.id}
	turn, present := runtimerequestctx.TurnMetaFromContext(ctx)
	turnID := strings.TrimSpace(turn.TurnID)
	if !present || turnID == "" {
		return decision, fmt.Errorf("tool execution protection: tool %q requires nonempty TurnMeta.TurnID", canonical)
	}
	selected := rule.selectArguments(args)
	if len(selected) == 0 {
		return decision, fmt.Errorf("tool execution protection: semantic argument selection for rule %q is empty", rule.id)
	}
	canonicalArgs, err := CanonicalJSON(selected)
	if err != nil {
		return decision, fmt.Errorf("tool execution protection: canonicalize rule %q arguments: %w", rule.id, err)
	}
	semanticSum := sha256.Sum256(canonicalArgs)
	semanticHash := hex.EncodeToString(semanticSum[:])
	claimKey, err := ClaimKey(rule.id, canonical, turnID, semanticHash)
	if err != nil {
		return decision, fmt.Errorf("tool execution protection: build claim key: %w", err)
	}
	decision.ClaimKey = claimKey
	if s.repository == nil {
		return decision, fmt.Errorf("tool execution protection: claim repository unavailable")
	}
	acquired, err := s.repository.Claim(ctx, ClaimRecord{
		ClaimKey:          claimKey,
		RuleID:            rule.id,
		CanonicalToolName: canonical,
		TurnID:            turnID,
		SemanticHash:      semanticHash,
		CreatedAt:         time.Now().UTC(),
	})
	if err != nil {
		return decision, fmt.Errorf("tool execution protection: persist claim for rule %q: %w", rule.id, err)
	}
	decision.Acquired = acquired
	return decision, nil
}

func (s *Service) Finish(ctx context.Context, claim toolprotection.Claim, state toolprotection.State) {
	if s == nil || s.repository == nil || !claim.Protected || !claim.Acquired || claim.ClaimKey == "" {
		return
	}
	_ = s.repository.Finish(ctx, claim.ClaimKey, state, time.Now().UTC())
}

func (r compiledRule) selectArguments(args map[string]interface{}) map[string]interface{} {
	selected := make(map[string]interface{})
	if r.fields == nil {
		for key, value := range args {
			if _, excluded := r.excludes[key]; !excluded {
				selected[key] = value
			}
		}
		return selected
	}
	for key := range r.fields {
		if _, excluded := r.excludes[key]; excluded {
			continue
		}
		if value, present := args[key]; present {
			selected[key] = value
		}
	}
	return selected
}

func canonicalRuntimeToolName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if index := strings.IndexByte(name, '|'); index >= 0 {
		name = strings.TrimSpace(name[:index])
	}
	canonical, err := execconfig.CanonicalProtectedToolName(name)
	return canonical, err == nil
}

// ClaimKey computes the length-prefixed v1 claim identity.
func ClaimKey(ruleID, canonicalToolName, turnID, semanticHash string) (string, error) {
	if len(semanticHash) != sha256.Size*2 || strings.ToLower(semanticHash) != semanticHash {
		return "", fmt.Errorf("semantic request hash must be 64-character lowercase hex")
	}
	if _, err := hex.DecodeString(semanticHash); err != nil {
		return "", fmt.Errorf("semantic request hash: %w", err)
	}
	hash := sha256.New()
	for _, part := range []string{claimKeyVersion, ruleID, canonicalToolName, turnID, semanticHash} {
		if !utf8.ValidString(part) {
			return "", fmt.Errorf("claim key part is not valid UTF-8")
		}
		if uint64(len(part)) > uint64(^uint32(0)) {
			return "", fmt.Errorf("claim key part is too large")
		}
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
