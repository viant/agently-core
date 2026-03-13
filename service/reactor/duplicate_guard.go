package reactor

import (
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
	mu          sync.Mutex
	lastKey     toolKey
	consecutive int
	window      []toolKey
	latest      map[toolKey]llm.ToolCall
}

const (
	consecutiveLimit = 3
	windowSize       = 8
	windowFreqLimit  = 5
)

func NewDuplicateGuard(prior []llm.ToolCall) *DuplicateGuard {
	g := &DuplicateGuard{
		latest: make(map[toolKey]llm.ToolCall, len(prior)),
		window: make([]toolKey, 0, windowSize),
	}
	for _, r := range prior {
		key := g.key(r.Name, r.Arguments)
		g.latest[key] = r
		g.window = append(g.window, key)
		g.lastKey = key
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

	if key == g.lastKey && prev.Name != "" && prev.Error == "" {
		return true, prev
	}

	g.updateConsecutive(key)
	g.updateWindow(key)

	if g.consecutive >= consecutiveLimit {
		return true, prev
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
}
