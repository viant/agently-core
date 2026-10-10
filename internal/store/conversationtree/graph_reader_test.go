package conversationtree

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	compact "github.com/viant/agently-core/internal/datly/conversation/graph/read"
	legacy "github.com/viant/agently-core/internal/datly/conversation/read"
	dexec "github.com/viant/datly/exec"
)

func TestPinGraphReader(t *testing.T) {
	for _, value := range []string{"", "legacy", "compact", " compact ", "invalid", "COMPACT"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(GraphReaderEnvironment, value)
			ctx, err := PinGraphReader(context.Background())
			if value == "invalid" || value == "COMPACT" {
				if err == nil {
					t.Fatal("invalid reader configuration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := graphReaderCompact
			if value == "legacy" {
				want = graphReaderLegacy
			}
			if ctx.Value(graphReaderContextKey{}) != want {
				t.Fatalf("unexpected reader: %v", ctx.Value(graphReaderContextKey{}))
			}
			t.Setenv(GraphReaderEnvironment, "invalid")
			nested, err := PinGraphReader(ctx)
			if err != nil || nested != ctx {
				t.Fatal("nested operation changed the pinned choice")
			}
		})
	}
}

func TestCompactGraphReaderLocksInBatchesAndRetainsPinnedMode(t *testing.T) {
	t.Setenv(GraphReaderEnvironment, "compact")
	ctx, err := PinGraphReader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(GraphReaderEnvironment, "legacy")
	graph := &Graph{Nodes: map[string]*Node{}}
	for i := 0; i < 501; i++ {
		id := fmt.Sprintf("conv-%03d", i)
		graph.Nodes[id] = &Node{ID: id, OwnerID: "u1"}
	}
	var sizes []int
	d := &Discoverer{OwnerID: func(context.Context) string { return "u1" }, Invoker: mutationTestInvoker(func(ctx context.Context, request dexec.ComponentRequest) (any, error) {
		if request.Target != compactGraphReaderTarget {
			t.Fatal("pinned compact mode switched to legacy")
		}
		if request.ReaderOptions == nil || !reflect.DeepEqual(request.ReaderOptions.ForUpdate, []string{dexec.RootView}) {
			t.Fatal("root row lock option missing")
		}
		input := request.Input.(*compact.Input)
		sizes = append(sizes, len(input.IDs))
		var trusted bool
		for _, p := range request.Providers {
			if p.Kind() == "conversationtreescope" {
				value, found, err := p.Locate(nil).Value(ctx, reflect.TypeFor[bool](), "internal")
				if err != nil || !found {
					t.Fatal("trusted host scope missing")
				}
				trusted = value.(bool)
			}
		}
		if !trusted {
			t.Fatal("trusted host scope is false")
		}
		out := &compact.Output{}
		for _, id := range input.IDs {
			out.Data = append(out.Data, &compact.Conversation{Id: id})
		}
		return out, nil
	})}
	if err := d.LockConversationGraph(ctx, graph); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sizes, []int{500, 1}) {
		t.Fatalf("lock batches=%v", sizes)
	}
}

func TestCompactGraphReaderLockRejectsMissingRows(t *testing.T) {
	t.Setenv(GraphReaderEnvironment, "compact")
	d := &Discoverer{OwnerID: func(context.Context) string { return "u1" }, Invoker: mutationTestInvoker(func(context.Context, dexec.ComponentRequest) (any, error) {
		return &compact.Output{}, nil
	})}
	if err := d.LockConversationGraph(context.Background(), &Graph{Nodes: map[string]*Node{"gone": {ID: "gone", OwnerID: "u1"}}}); err != ErrNotFound {
		t.Fatalf("missing locked row: %v", err)
	}
}

func TestCompactGraphReaderDiscoveryIsUnlockedAndMapsOnlyGraphFields(t *testing.T) {
	owner, status, run, created := "u1", "succeeded", "run", "2026-01-01T00:00:00Z"
	d := &Discoverer{Invoker: mutationTestInvoker(func(_ context.Context, request dexec.ComponentRequest) (any, error) {
		if request.ReaderOptions == nil || len(request.ReaderOptions.ForUpdate) != 0 {
			t.Fatal("discovery inherited a row lock")
		}
		if input := request.Input.(*compact.Input); !reflect.DeepEqual(input.ParentTurnIDs, []string{"turn"}) {
			t.Fatalf("wrong bounded predicate: %+v", input)
		}
		return &compact.Output{Data: []*compact.Conversation{{Id: "conv", CreatedByUserId: &owner, Status: &status, ScheduleRunId: &run, CreatedAtRaw: &created}}}, nil
	})}
	query := &legacy.ConversationInput{}
	query.SetParentTurnIds([]string{"turn"})
	rows, err := d.compactGraphRows(context.Background(), query, false)
	if err != nil || len(rows) != 1 || rows[0].CreatedAtRaw == nil || *rows[0].CreatedAtRaw != created || rows[0].Status == nil || *rows[0].Status != status {
		t.Fatalf("graph mapping: %+v %v", rows, err)
	}
	if _, err := d.compactGraphRows(context.Background(), &legacy.ConversationInput{}, false); err == nil {
		t.Fatal("unbounded query accepted")
	}
}
