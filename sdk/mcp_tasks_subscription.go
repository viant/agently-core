package sdk

import (
	"context"
	"fmt"
	"sync"

	"github.com/viant/agently-core/runtime/streaming"
	tasks "github.com/viant/mcp-tasks"
)

// WatchTasks attaches only owned native execution hints. Generic subscriptions
// re-read Get after every hint and periodically; no event payload reaches MCP.
func (b *MCPTasksBackend) WatchTasks(ctx context.Context, owner tasks.Owner, ids []string) (tasks.Wakeup, error) {
	if len(ids) == 0 || len(ids) > tasks.MaximumSubscriptionTasks {
		return nil, fmt.Errorf("bounded task subscription required")
	}
	references := make([]MCPTaskReference, 0, len(ids))
	for _, id := range ids {
		_, alias, _, err := b.load(ctx, owner, id)
		if err != nil {
			return nil, err
		}
		reference, err := b.current(ctx, owner, alias.Reference)
		if err != nil {
			return nil, err
		}
		if err = b.controls.Authorize(ctx, owner, reference); err != nil {
			return nil, tasks.ErrNotFound
		}
		run, err := b.store.GetRun(ctx, owner.Subject, reference.ThreadID, reference.RunID)
		if err != nil {
			return nil, err
		}
		reference.ConversationID = run.ConversationID
		reference.TurnID = run.TurnID
		references = append(references, reference)
	}
	if b.controls.Watch == nil {
		return nil, fmt.Errorf("native task event subscription is not configured")
	}
	return b.controls.Watch(ctx, owner, references)
}

type mcpTaskWakeup struct {
	ctx     context.Context
	cancel  context.CancelFunc
	channel chan struct{}
	sources []streaming.Subscription
	once    sync.Once
	done    chan struct{}
}

func (w *mcpTaskWakeup) C() <-chan struct{} { return w.channel }
func (w *mcpTaskWakeup) Close() error {
	w.once.Do(func() {
		w.cancel()
		for _, source := range w.sources {
			_ = source.Close()
		}
	})
	<-w.done
	return nil
}
func (w *mcpTaskWakeup) hint() {
	select {
	case w.channel <- struct{}{}:
	default:
	}
}
func mcpTasksAGUIWatch(client Client) func(context.Context, tasks.Owner, []MCPTaskReference) (tasks.Wakeup, error) {
	return func(parent context.Context, _ tasks.Owner, references []MCPTaskReference) (tasks.Wakeup, error) {
		ctx, cancel := context.WithCancel(parent)
		w := &mcpTaskWakeup{ctx: ctx, cancel: cancel, channel: make(chan struct{}, 1), done: make(chan struct{})}
		grouped := map[string]map[string]bool{}
		for _, reference := range references {
			if reference.ConversationID == "" {
				cancel()
				return nil, fmt.Errorf("native task event scope unavailable")
			}
			if grouped[reference.ConversationID] == nil {
				grouped[reference.ConversationID] = map[string]bool{}
			}
			grouped[reference.ConversationID][reference.TurnID] = true
		}
		for conversation, turns := range grouped {
			conversation, turns := conversation, turns
			source, err := subscribeNativeEvents(ctx, client, &StreamEventsInput{ConversationID: conversation, Filter: func(event *streaming.Event) bool {
				if event == nil || (event.ConversationID != conversation && !(event.ConversationID == "" && event.StreamID == conversation)) {
					return false
				}
				// Journal post-commit hints have no turn ID. Both them and scoped native
				// run/input/terminal events wake an authoritative task state read.
				return event.TurnID == "" || turns[event.TurnID]
			}})
			if err != nil {
				cancel()
				for _, sub := range w.sources {
					_ = sub.Close()
				}
				return nil, err
			}
			w.sources = append(w.sources, source)
		}
		var workers sync.WaitGroup
		for _, source := range w.sources {
			workers.Add(1)
			go func(source streaming.Subscription) {
				defer workers.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case _, open := <-source.C():
						w.hint()
						if !open {
							return
						}
					}
				}
			}(source)
		}
		go func() { workers.Wait(); close(w.channel); close(w.done) }()
		return w, nil
	}
}
