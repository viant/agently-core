package matcher

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/viant/agently-core/genai/llm"
	asynccfg "github.com/viant/agently-core/protocol/async"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
)

// Frozen pre-preparation matching behavior, including its legacy prefix fallback.
func legacyMatch(pattern, name string) bool {
	pcanon, ncanon := Canon(pattern), Canon(name)
	if pcanon == "*" {
		return true
	}
	if pcanon == ncanon {
		return true
	}
	if strings.Contains(pcanon, "*") {
		return strings.HasPrefix(ncanon, strings.TrimSuffix(pcanon, "*"))
	}
	raw := strings.TrimSpace(pattern)
	if raw != "" && !strings.Contains(raw, ":") && !strings.Contains(raw, "*") {
		return strings.HasPrefix(ncanon, pcanon)
	}
	if strings.HasPrefix(ncanon, pcanon) {
		return true
	}
	return false
}

func TestPreparedPatternPreservesLegacyMatching(t *testing.T) {
	values := []string{"", " ", "*", "a", "a:b", "a/b", "a-b", "a.b", "a_b", " a/b:read ", "a/b-read", "a/b/read", "a:*", "a/b:*", "a:read*", "a:*read", "a:r*ad", "a:r*ad*", "a:r**", "**", "[abc]", "?", "東京/分析:方法", "東京/分析:方*", "é:Lire", "é:lire", "x\xff:y", "a:b\n"}
	for _, pattern := range values {
		for _, name := range values {
			assertPreparedEquivalent(t, pattern, name)
		}
	}
	random := rand.New(rand.NewSource(54517))
	alphabet := []rune("abAB 09/:.-_*?[]é東京")
	word := func() string {
		out := make([]rune, random.Intn(25))
		for i := range out {
			out[i] = alphabet[random.Intn(len(alphabet))]
		}
		return string(out)
	}
	for i := 0; i < 10000; i++ {
		assertPreparedEquivalent(t, word(), word())
	}
}
func assertPreparedEquivalent(t *testing.T, pattern, name string) {
	t.Helper()
	want := legacyMatch(pattern, name)
	prepared := Prepare(pattern)
	if got := prepared.MatchCanonical(Canon(name)); got != want {
		t.Fatalf("prepared(%q,%q)=%v want %v", pattern, name, got, want)
	}
	if got := Match(pattern, name); got != want {
		t.Fatalf("Match(%q,%q)=%v want %v", pattern, name, got, want)
	}
}

func TestPreparedPatternKeysPreserveAliasEquivalence(t *testing.T) {
	groups := [][]string{{"catalog:read", "catalog.read", "catalog/read", "catalog-read", " catalog:read "}, {"a/b:read", "a_b-read", "a/b/read", "a/b.read"}, {"", " "}, {"é:方法*", "é/方法*", "é-方法*"}}
	names := []string{"", "catalog:read", "catalog:readMore", "catalog:write", "a/b:read", "a/b:readMore", "é:方法", "é:方法More", "other:read"}
	for _, group := range groups {
		for _, pattern := range group {
			if Prepare(group[0]).Key() != Prepare(pattern).Key() {
				t.Fatalf("not equivalent: %q %q", group[0], pattern)
			}
			for _, name := range names {
				if legacyMatch(group[0], name) != legacyMatch(pattern, name) {
					t.Fatalf("key collision changes behavior: %q %q %q", group[0], pattern, name)
				}
			}
		}
	}
}

func FuzzPreparedPatternLegacy(f *testing.F) {
	for _, pair := range [][2]string{{"", ""}, {"*", "x:y"}, {"a/b:*", "a_b-read"}, {"a:r*ad*", "a:r*adMore"}, {"東京/分析:方*", "東京/分析:方法"}, {"x\xff:y", "x\xff:y"}} {
		f.Add(pair[0], pair[1])
	}
	f.Fuzz(func(t *testing.T, pattern, name string) { assertPreparedEquivalent(t, pattern, name) })
}

func preparedResolutionFixture() ([]llm.ToolDefinition, *toolbundle.Bundle) {
	defs := []llm.ToolDefinition{}
	for _, name := range []string{"catalog:read", "catalog:readMore", "catalog:secret", "東京/分析:方法"} {
		defs = append(defs, llm.ToolDefinition{Name: name, Description: "Neutral operation", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"filter": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}}}, Required: []string{"filter"}, OutputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"items": map[string]interface{}{"type": "array"}}}, Strict: true, Cacheable: true, Tier: llm.ToolTierBase, DiscoveryNamespace: "analysis", SourceMCPServer: "connection", SourceResourceURI: "mcp://analysis/catalog", SourceProviderIdentity: "catalog-owner"})
	}
	bundle := &toolbundle.Bundle{ID: "neutral", Match: []llm.Tool{
		{Name: "catalog:*", Exclude: []string{"catalog:secret"}, Tier: llm.ToolTierOnDemand},
		{Name: "catalog:read", AlwaysInclude: true, Tier: llm.ToolTierCritical, Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue}, Async: &asynccfg.Config{TimeoutMs: 1200, Narration: "none"}},
		{Name: "catalog.read", Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModePrompt, Prompt: &llm.ApprovalPrompt{Message: "Review"}}},
		{Name: "catalog/r*ad*"}, {Name: "東京/分析:方*"}, {Name: " "},
	}}
	return defs, bundle
}

func TestPreparedResolutionPreservesCompleteMetadataAndInputs(t *testing.T) {
	defs, bundle := preparedResolutionFixture()
	beforeDefs, beforeBundle := preparedResolutionFixture()
	original := toolbundle.ResolveDefinitionsWithOptions(bundle, func(pattern string) []*llm.ToolDefinition {
		var out []*llm.ToolDefinition
		for i := range defs {
			if legacyMatch(pattern, defs[i].Name) {
				out = append(out, &defs[i])
			}
		}
		return out
	})
	names := make([]string, len(defs))
	for i := range defs {
		names[i] = Canon(defs[i].Name)
	}
	memo := map[string][]*llm.ToolDefinition{}
	actual := toolbundle.ResolveDefinitionsWithOptions(bundle, func(pattern string) []*llm.ToolDefinition {
		prepared := Prepare(pattern)
		if got, ok := memo[prepared.Key()]; ok {
			return got
		}
		var out []*llm.ToolDefinition
		for i := range defs {
			if prepared.MatchCanonical(names[i]) {
				out = append(out, &defs[i])
			}
		}
		memo[prepared.Key()] = out
		return out
	})
	if !reflect.DeepEqual(original, actual) {
		t.Fatalf("complete resolution changed:\nold=%#v\nnew=%#v", original, actual)
	}
	if !reflect.DeepEqual(beforeDefs, defs) || !reflect.DeepEqual(beforeBundle, bundle) {
		t.Fatal("resolution mutated its input catalog or rules")
	}
	if len(actual.ApprovalByID) != 2 || len(actual.AsyncByID) != 2 {
		t.Fatalf("missing rule metadata: %#v", actual)
	}
}
