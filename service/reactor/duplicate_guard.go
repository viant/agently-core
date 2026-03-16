package reactor

import (
	"strings"
	"sync"

	"github.com/viant/agently-core/genai/llm"
)

type toolKey struct {
	Name string
	Args string
}

// DuplicateGuard tracks recent tool-call patterns and blocks pathological
// repetition without inventing a new transcript shape. When a prior successful
// result exists, callers may synthesize that same result instead of re-running
// the tool.
type DuplicateGuard struct {
	mu           sync.Mutex
	lastKey      toolKey
	consecutive  int
	lastName     string
	nameStreak   int
	window       []toolKey
	latest       map[toolKey]llm.ToolCall
	latestByName map[string]llm.ToolCall
}

const (
	consecutiveLimit = 3
	windowSize       = 8
	windowFreqLimit  = 5
)

func NewDuplicateGuard(prior []llm.ToolCall) *DuplicateGuard {
	g := &DuplicateGuard{
		latest:       make(map[toolKey]llm.ToolCall, len(prior)),
		latestByName: make(map[string]llm.ToolCall, len(prior)),
		window:       make([]toolKey, 0, windowSize),
	}
	for _, r := range prior {
		key := g.key(r.Name, r.Arguments)
		g.latest[key] = r
		g.latestByName[g.normalizedName(r.Name)] = r
		g.window = append(g.window, key)
		g.lastKey = key
		g.updateNameStreak(r.Name)
	}
	return g
}

func (g *DuplicateGuard) key(name string, args map[string]interface{}) toolKey {
	return toolKey{Name: name, Args: CanonicalArgs(args)}
}

func (g *DuplicateGuard) ShouldBlock(name string, args map[string]interface{}) (bool, llm.ToolCall) {
	g.mu.Lock()
	defer g.mu.Unlock()

	key := g.key(name, args)
	prev := g.latest[key]
	prevByName := g.latestByName[g.normalizedName(name)]

	if key == g.lastKey && prev.Name != "" && prev.Error == "" {
		return true, prev
	}
	if g.isRootListTool(name) && prevByName.Name != "" && prevByName.Error == "" {
		return true, prevByName
	}

	g.updateConsecutive(key)
	g.updateNameStreak(name)
	g.updateWindow(key)

	if g.consecutive >= consecutiveLimit {
		return true, prev
	}
	if g.isPlanTool(name) && g.nameStreak >= 2 && prevByName.Name != "" && prevByName.Error == "" {
		return true, prevByName
	}
	if g.frequency(key) >= windowFreqLimit {
		return true, prev
	}
	if g.isAlternatingPattern() {
		return true, prev
	}
	return false, llm.ToolCall{}
}

func (g *DuplicateGuard) updateConsecutive(k toolKey) {
	if k == g.lastKey {
		g.consecutive++
	} else {
		g.consecutive = 1
		g.lastKey = k
	}
}

func (g *DuplicateGuard) updateNameStreak(name string) {
	normalized := g.normalizedName(name)
	if normalized == g.lastName {
		g.nameStreak++
	} else {
		g.lastName = normalized
		g.nameStreak = 1
	}
}

func (g *DuplicateGuard) updateWindow(k toolKey) {
	g.window = append(g.window, k)
	if len(g.window) > windowSize {
		g.window = g.window[len(g.window)-windowSize:]
	}
}

func (g *DuplicateGuard) frequency(k toolKey) int {
	count := 0
	for _, w := range g.window {
		if w == k {
			count++
		}
	}
	return count
}

func (g *DuplicateGuard) isAlternatingPattern() bool {
	if len(g.window) < windowSize {
		return false
	}
	distinct := map[toolKey]struct{}{}
	for _, w := range g.window {
		distinct[w] = struct{}{}
	}
	if len(distinct) != 2 {
		return false
	}
	for i := 2; i < len(g.window); i++ {
		if g.window[i] != g.window[i-2] {
			return false
		}
	}
	return true
}

func (g *DuplicateGuard) RegisterResult(name string, args map[string]interface{}, res llm.ToolCall) {
	g.mu.Lock()
	defer g.mu.Unlock()

	k := g.key(name, args)
	g.latest[k] = res
	g.lastKey = k
	normalized := g.normalizedName(name)
	g.latestByName[normalized] = res
	g.updateNameStreak(name)
}

func (g *DuplicateGuard) normalizedName(name string) string {
	text := strings.ToLower(strings.TrimSpace(name))
	text = strings.ReplaceAll(text, ":", "/")
	text = strings.ReplaceAll(text, "_", "/")
	text = strings.ReplaceAll(text, "-", "/")
	return text
}

func (g *DuplicateGuard) isPlanTool(name string) bool {
	return g.normalizedName(name) == "orchestration/updateplan"
}

func (g *DuplicateGuard) isRootListTool(name string) bool {
	return g.normalizedName(name) == "resources/list" || g.normalizedName(name) == "resources/roots"
}
