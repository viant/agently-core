package reactor

import (
	"sync"
	"testing"

	"github.com/viant/agently-core/genai/llm"
)

func TestDuplicateGuard_ShouldBlock(t *testing.T) {
	tests := []struct {
		name         string
		priorResults []llm.ToolCall
		callName     string
		callArgs     map[string]interface{}
		wantBlock    bool
	}{
		{
			name: "block when prior successful result exists",
			priorResults: []llm.ToolCall{{
				Name:      "sqlkit-dbListConnections",
				Arguments: map[string]interface{}{"pattern": "*"},
				Result:    "[{\"name\":\"dev\"}]",
			}},
			callName:  "sqlkit-dbListConnections",
			callArgs:  map[string]interface{}{"pattern": "*"},
			wantBlock: true,
		},
		{
			name: "allow retry when previous result had error",
			priorResults: []llm.ToolCall{{
				Name:      "sqlkit-dbListConnections",
				Arguments: map[string]interface{}{"pattern": "*"},
				Error:     "connection timeout",
			}},
			callName:  "sqlkit-dbListConnections",
			callArgs:  map[string]interface{}{"pattern": "*"},
			wantBlock: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			guard := NewDuplicateGuard(tc.priorResults)
			gotBlock, _ := guard.ShouldBlock(tc.callName, tc.callArgs)
			if gotBlock != tc.wantBlock {
				t.Fatalf("expected block=%v, got %v", tc.wantBlock, gotBlock)
			}
		})
	}
}

func TestDuplicateGuard_BlocksRepeatedResourcesListByName(t *testing.T) {
	guard := NewDuplicateGuard(nil)
	argsA := map[string]interface{}{"rootId": "local", "path": "/repo", "maxItems": 200}
	firstBlock, _ := guard.ShouldBlock("resources/list", argsA)
	if firstBlock {
		t.Fatalf("first resources/list should not be blocked")
	}
	guard.RegisterResult("resources/list", argsA, llm.ToolCall{
		Name:      "resources/list",
		Arguments: argsA,
		Result:    "ok",
	})

	secondBlock, reused := guard.ShouldBlock("resources/list", map[string]interface{}{"rootId": "local", "path": "/repo", "maxItems": 100})
	if !secondBlock {
		t.Fatalf("repeated resources/list should be blocked")
	}
	if reused.Name != "resources/list" {
		t.Fatalf("expected resources/list result to be reused, got %q", reused.Name)
	}
}

func TestDuplicateGuard_ConsecutiveCalls(t *testing.T) {
	type call struct {
		Name      string
		Args      map[string]interface{}
		Error     string
		wantBlock bool
	}

	tests := []struct {
		name     string
		sequence []call
	}{
		{
			name: "block third consecutive identical call",
			sequence: []call{
				{Name: "sqlkit-query", Args: map[string]interface{}{"query": "SELECT * FROM users"}, wantBlock: false},
				{Name: "sqlkit-query", Args: map[string]interface{}{"query": "SELECT * FROM user"}, Error: "some error", wantBlock: false},
				{Name: "sqlkit-query", Args: map[string]interface{}{"query": "SELECT * FROM user"}, Error: "some error", wantBlock: false},
				{Name: "sqlkit-query", Args: map[string]interface{}{"query": "SELECT * FROM user"}, Error: "some error", wantBlock: true},
			},
		},
		{
			name: "reset consecutive counter on different call",
			sequence: []call{
				{Name: "sqlkit-query", Args: map[string]interface{}{"query": "SELECT * FROM user"}, Error: "some error", wantBlock: false},
				{Name: "sqlkit-query", Args: map[string]interface{}{"query": "SELECT * FROM user"}, Error: "some error", wantBlock: false},
				{Name: "sqlkit-query", Args: map[string]interface{}{"query": "SELECT * FROM orders"}, Error: "some error", wantBlock: false},
				{Name: "sqlkit-query", Args: map[string]interface{}{"query": "SELECT * FROM user"}, Error: "some error", wantBlock: false},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			guard := NewDuplicateGuard(nil)
			for i, call := range tc.sequence {
				gotBlock, _ := guard.ShouldBlock(call.Name, call.Args)
				if gotBlock != call.wantBlock {
					t.Fatalf("call %d: expected block=%v, got %v", i, call.wantBlock, gotBlock)
				}
				if !gotBlock {
					guard.RegisterResult(call.Name, call.Args, llm.ToolCall{
						Name:      call.Name,
						Arguments: call.Args,
						Result:    "some result",
						Error:     call.Error,
					})
				}
			}
		})
	}
}

func TestDuplicateGuard_WindowFrequency(t *testing.T) {
	type call struct {
		Name      string
		Args      map[string]interface{}
		Error     string
		wantBlock bool
	}

	sequence := []call{
		{Name: "a", Args: map[string]interface{}{"a": ""}, wantBlock: false},
		{Name: "c", Args: map[string]interface{}{"c": ""}, wantBlock: false},
		{Name: "X", Args: map[string]interface{}{"X": ""}, wantBlock: false},
		{Name: "a", Args: map[string]interface{}{"a": ""}, wantBlock: false},
		{Name: "X", Args: map[string]interface{}{"X": ""}, wantBlock: false},
		{Name: "b", Args: map[string]interface{}{"b": ""}, wantBlock: false},
		{Name: "X", Args: map[string]interface{}{"X": ""}, Error: "E", wantBlock: false},
		{Name: "X", Args: map[string]interface{}{"X": ""}, wantBlock: false},
		{Name: "d", Args: map[string]interface{}{"d": ""}, wantBlock: false},
		{Name: "X", Args: map[string]interface{}{"X": ""}, wantBlock: true},
	}

	guard := NewDuplicateGuard(nil)
	for i, call := range sequence {
		gotBlock, _ := guard.ShouldBlock(call.Name, call.Args)
		if gotBlock != call.wantBlock {
			t.Fatalf("call %d: expected block=%v, got %v", i, call.wantBlock, gotBlock)
		}
		if !gotBlock {
			guard.RegisterResult(call.Name, call.Args, llm.ToolCall{
				Name:      call.Name,
				Arguments: call.Args,
				Result:    "some result",
				Error:     call.Error,
			})
		}
	}
}

func TestDuplicateGuard_AlternatingPattern(t *testing.T) {
	guard := NewDuplicateGuard(nil)
	for i := 0; i < 8; i++ {
		name := "call-A"
		args := map[string]interface{}{"type": "A"}
		want := false
		if i%2 == 1 {
			name = "call-B"
			args = map[string]interface{}{"type": "B"}
			want = i == 7
		}
		gotBlock, _ := guard.ShouldBlock(name, args)
		if gotBlock != want {
			t.Fatalf("call %d: expected block=%v, got %v", i, want, gotBlock)
		}
		if !gotBlock {
			guard.RegisterResult(name, args, llm.ToolCall{
				Name:      name,
				Arguments: args,
				Result:    "some result",
			})
		}
	}
}

func TestDuplicateGuard_BlocksRepeatedUpdatePlanEvenWhenArgsChange(t *testing.T) {
	guard := NewDuplicateGuard(nil)

	firstBlock, _ := guard.ShouldBlock("orchestration/updatePlan", map[string]interface{}{
		"plan": []map[string]interface{}{{"step": "scan repo", "status": "in_progress"}},
	})
	if firstBlock {
		t.Fatalf("first updatePlan should not be blocked")
	}
	guard.RegisterResult("orchestration/updatePlan", map[string]interface{}{
		"plan": []map[string]interface{}{{"step": "scan repo", "status": "in_progress"}},
	}, llm.ToolCall{
		Name:   "orchestration/updatePlan",
		Result: `{"plan":[{"step":"scan repo","status":"in_progress"}]}`,
	})

	secondBlock, reused := guard.ShouldBlock("orchestration/updatePlan", map[string]interface{}{
		"plan": []map[string]interface{}{{"step": "inspect packages", "status": "in_progress"}},
	})
	if !secondBlock {
		t.Fatalf("second consecutive updatePlan should be blocked even when args change")
	}
	if reused.Name != "orchestration/updatePlan" {
		t.Fatalf("expected previous updatePlan result to be reused, got %q", reused.Name)
	}
}

func TestDuplicateGuard_Args(t *testing.T) {
	tests := []struct {
		name     string
		sequence []struct {
			name      string
			args      map[string]interface{}
			wantBlock bool
		}
	}{
		{
			name: "different argument order should be treated as same",
			sequence: []struct {
				name      string
				args      map[string]interface{}
				wantBlock bool
			}{
				{name: "query", args: map[string]interface{}{"param1": "value1", "param2": "value2"}, wantBlock: false},
				{name: "query", args: map[string]interface{}{"param2": "value2", "param1": "value1"}, wantBlock: true},
			},
		},
		{
			name: "empty arguments should be handled correctly",
			sequence: []struct {
				name      string
				args      map[string]interface{}
				wantBlock bool
			}{
				{name: "no-args-call", args: map[string]interface{}{}, wantBlock: false},
				{name: "no-args-call", args: nil, wantBlock: true},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			guard := NewDuplicateGuard(nil)
			for i, call := range tc.sequence {
				gotBlock, _ := guard.ShouldBlock(call.name, call.args)
				if gotBlock != call.wantBlock {
					t.Fatalf("call %d: expected block=%v, got %v", i, call.wantBlock, gotBlock)
				}
				if !gotBlock {
					guard.RegisterResult(call.name, call.args, llm.ToolCall{
						Name:      call.name,
						Arguments: call.args,
						Result:    "some result",
					})
				}
			}
		})
	}
}

func TestDuplicateGuard_ConcurrentAccess(t *testing.T) {
	guard := NewDuplicateGuard(nil)

	const goroutines = 32
	const iterations = 50

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for goroutineID := 0; goroutineID < goroutines; goroutineID++ {
		go func(id int) {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				name := "tool"
				args := map[string]interface{}{"goroutine": id, "i": i}
				guard.ShouldBlock(name, args)
				guard.RegisterResult(name, args, llm.ToolCall{
					Name:      name,
					Arguments: args,
					Result:    "ok",
				})
			}
		}(goroutineID)
	}

	close(start)
	wg.Wait()
}
