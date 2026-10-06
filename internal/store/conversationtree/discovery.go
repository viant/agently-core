package conversationtree

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	msgread "github.com/viant/agently-core/internal/datly/message/read"
	turnread "github.com/viant/agently-core/internal/datly/turn/read"
	conversation "github.com/viant/agently-core/internal/store/conversation"
	"github.com/viant/agently-core/internal/store/maintenancediag"

	"github.com/viant/agently-core/internal/datly/dbtime"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/xdatly/state"
)

const MaxConversations = 10_000

var ErrNotFound = errors.New("conversation not found")
var ErrTooLarge = errors.New("conversation graph exceeds the deletion limit")
var ErrPermissionDenied = errors.New("permission denied")

type Node struct {
	ID            string
	OwnerID       string
	Status        string
	ScheduleRunID string
	CreatedAt     time.Time
	Depth         int
}

type Graph struct {
	Nodes map[string]*Node
}

type Discoverer struct {
	Invoker dexec.ComponentInvoker
	OwnerID func(context.Context) string
	Schema  TableInspector
	// LockDetachRows is set only by the managed deletion transaction.
	LockDetachRows  bool
	systemRetention bool
	GraphFields     []string
}

// NewSystemDiscoverer preserves the system-retention policy that ignores
// historical owners. Its private maintenance parent must validate eligibility
// and fence a live maintenance lease before any mutation; references/liveness
// remain enforced. User components construct the ordinary strict-owner reader.
func NewSystemDiscoverer(invoker dexec.ComponentInvoker, schema TableInspector) *Discoverer {
	return &Discoverer{Invoker: invoker, Schema: schema, OwnerID: func(context.Context) string { return "" }, systemRetention: true,
		GraphFields: []string{"id", "created_by_user_id", "status", "schedule_run_id", "created_at_raw"}}
}

func (d *Discoverer) authorize(ctx context.Context, graph *Graph) error {
	if d == nil || graph == nil || d.OwnerID == nil {
		return ErrPermissionDenied
	}
	if d.systemRetention {
		return nil
	}
	return graph.AuthorizeOwner(d.OwnerID(ctx))
}

// DiscoverAuthorized applies the legacy all-nodes owner rule to the complete
// graph before a deletion orchestrator may lock or mutate any rows.
func (d *Discoverer) DiscoverAuthorized(ctx context.Context, rootIDs ...string) (*Graph, error) {
	graph, err := d.Discover(ctx, rootIDs...)
	if err != nil {
		return nil, err
	}
	if err := d.authorize(ctx, graph); err != nil {
		return nil, err
	}
	return graph, nil
}

func (g *Graph) AuthorizeOwner(userID string) error {
	if g == nil {
		return ErrPermissionDenied
	}
	if len(g.Nodes) == 0 {
		return nil
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ErrPermissionDenied
	}
	for _, node := range g.Nodes {
		if node == nil || strings.TrimSpace(node.OwnerID) == "" || strings.TrimSpace(node.OwnerID) != userID {
			return ErrPermissionDenied
		}
	}
	return nil
}

// Discover follows parent, parent-turn and linked-message edges in bounded
// batches. Every database read is a canonical generated Datly component.
func (d *Discoverer) Discover(ctx context.Context, rootIDs ...string) (result *Graph, retErr error) {
	done := maintenancediag.Phase(ctx, "graph_discovery")
	defer func() {
		count := 0
		if result != nil {
			count = len(result.Nodes)
		}
		done(retErr, fmt.Sprintf("conversations=%d", count))
	}()
	if d == nil || d.Invoker == nil || d.OwnerID == nil {
		return nil, fmt.Errorf("conversation graph reader is not configured")
	}
	roots := normalizeIDs(rootIDs)
	graph := &Graph{Nodes: map[string]*Node{}}
	if len(roots) == 0 {
		return graph, nil
	}
	if len(roots) > MaxConversations {
		return nil, ErrTooLarge
	}
	conversations := &conversation.Store{Invoker: d.Invoker, OwnerID: d.OwnerID}
	turns := &conversation.TurnStore{Invoker: d.Invoker}
	messages := &conversation.MessageStore{Invoker: d.Invoker, OwnerID: d.OwnerID}
	rootQuery := &convread.ConversationInput{}
	rootQuery.SetIds(roots)
	rootRows, err := conversations.GraphRows(ctx, rootQuery, d.GraphFields)
	if err != nil {
		return nil, err
	}
	if len(rootRows) != len(roots) {
		return nil, ErrNotFound
	}
	frontier := roots
	for _, row := range rootRows {
		graph.add(row, 0)
	}
	for depth := 1; len(frontier) > 0; depth++ {
		parentQuery := &convread.ConversationInput{}
		parentQuery.SetParentIds(frontier)
		children, err := conversations.GraphRows(ctx, parentQuery, d.GraphFields)
		if err != nil {
			return nil, err
		}
		turnQuery := &turnread.TurnRowsInput{}
		turnQuery.SetConversationIDs(frontier)
		turnRows, err := turns.ListRows(ctx, turnQuery, state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: []string{"id"}}}})
		if err != nil {
			return nil, err
		}
		turnIDs := make([]string, 0, len(turnRows))
		for _, row := range turnRows {
			if row != nil {
				turnIDs = append(turnIDs, row.Id)
			}
		}
		if turnIDs = normalizeIDs(turnIDs); len(turnIDs) > 0 {
			turnChildQuery := &convread.ConversationInput{}
			turnChildQuery.SetParentTurnIds(turnIDs)
			turnChildren, err := conversations.GraphRows(ctx, turnChildQuery, d.GraphFields)
			if err != nil {
				return nil, err
			}
			children = append(children, turnChildren...)
		}
		messageQuery := &msgread.MessagesInput{}
		messageQuery.SetConversationIds(frontier)
		selector := state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: []string{"id", "conversation_id", "linked_conversation_id"}}}}
		messageRows, err := messages.ListRows(ctx, messageQuery, selector)
		if err != nil {
			return nil, err
		}
		linkedIDs := make([]string, 0)
		for _, row := range messageRows {
			if row != nil && row.LinkedConversationId != nil {
				linkedIDs = append(linkedIDs, *row.LinkedConversationId)
			}
		}
		if linkedIDs = normalizeIDs(linkedIDs); len(linkedIDs) > 0 {
			linkedQuery := &convread.ConversationInput{}
			linkedQuery.SetIds(linkedIDs)
			linked, err := conversations.GraphRows(ctx, linkedQuery, d.GraphFields)
			if err != nil {
				return nil, err
			}
			children = append(children, linked...)
		}
		next := make([]string, 0, len(children))
		for _, row := range children {
			if row == nil || graph.Nodes[row.Id] != nil {
				continue
			}
			if len(graph.Nodes) >= MaxConversations {
				return nil, ErrTooLarge
			}
			graph.add(row, depth)
			next = append(next, row.Id)
		}
		frontier = normalizeIDs(next)
	}
	return graph, nil
}

func (g *Graph) add(row *convread.ConversationView, depth int) {
	if row == nil {
		return
	}
	node := &Node{ID: row.Id, CreatedAt: row.CreatedAt, Depth: depth}
	if node.CreatedAt.IsZero() && row.CreatedAtRaw != nil {
		if parsed, ok := dbtime.ParseActivity(*row.CreatedAtRaw); ok {
			node.CreatedAt = parsed
		}
	}
	if row.CreatedByUserId != nil {
		node.OwnerID = strings.TrimSpace(*row.CreatedByUserId)
	}
	if row.Status != nil {
		node.Status = strings.TrimSpace(*row.Status)
	}
	if row.ScheduleRunId != nil {
		node.ScheduleRunID = strings.TrimSpace(*row.ScheduleRunId)
	}
	g.Nodes[row.Id] = node
}

func normalizeIDs(ids []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}
