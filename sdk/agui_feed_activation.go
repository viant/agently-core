package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
)

// AGUIFeedActivationReader is a legacy optional bool-only source. It cannot
// establish original lifecycle ordering and is not used for durable recovery.
// A future authoritative provider must return original transition provenance.
type AGUIFeedActivationReader interface {
	AGUIFeedActivation(context.Context, string, string) (*bool, error)
}

type aguiFeedActivation struct {
	Active   *bool
	At       time.Time
	Origin   *agui.FeedLifecycleOrigin
	frontier []agui.FeedLifecycleFact
}

// compareFeedFacts is a partial order: run enumeration and refresh emission
// sequence are never a lifecycle clock. Equal/undated independent opposites are
// incomparable and remain in the frontier until genuine evidence dominates them.
func compareFeedFacts(left, right agui.FeedLifecycleFact) int {
	if !left.At.IsZero() && !right.At.IsZero() && !left.At.Equal(right.At) {
		if left.At.Before(right.At) {
			return -1
		}
		return 1
	}
	if left.At.IsZero() != right.At.IsZero() {
		return 2
	}
	if left.Origin != nil && right.Origin != nil && left.Origin.RunID == right.Origin.RunID && left.Origin.Sequence != right.Origin.Sequence {
		if left.Origin.Sequence < right.Origin.Sequence {
			return -1
		}
		return 1
	}
	sameSource := left.Origin == nil && right.Origin == nil || left.Origin != nil && right.Origin != nil && *left.Origin == *right.Origin
	if left.Active == right.Active && left.At.Equal(right.At) && sameSource {
		return 0
	}
	return 2
}

func (state *aguiFeedActivation) observe(fact agui.FeedLifecycleFact) {
	next := make([]agui.FeedLifecycleFact, 0, len(state.frontier)+1)
	for _, existing := range state.frontier {
		relation := compareFeedFacts(fact, existing)
		if relation == -1 || relation == 0 {
			return
		}
		if relation != 1 {
			next = append(next, existing)
		}
	}
	next = append(next, fact)
	state.frontier = next
	state.Active, state.At, state.Origin = nil, time.Time{}, nil
	active := next[0].Active
	for _, item := range next {
		if item.Active != active {
			state.At = time.Time{}
			return
		}
		if item.At.After(state.At) {
			state.At = item.At
		}
	}
	state.Active = &active
	if len(next) == 1 && next[0].Origin != nil {
		origin := *next[0].Origin
		state.Origin = &origin
	}
}

func recoverAGUIFeedActivation(ctx context.Context, _ Client, store aguistore.Store, record *aguistore.Run, id string) (aguiFeedActivation, error) {
	var facts []agui.FeedLifecycleFact
	if reader, ok := store.(aguistore.FeedActivationJournalReader); ok {
		var err error
		facts, err = reader.ReadFeedActivationFacts(ctx, record.Principal, record.ThreadID, id)
		if err != nil {
			return aguiFeedActivation{}, fmt.Errorf("complete feed lifecycle snapshot unavailable: %w", err)
		}
	} else {
		// Custom stores retain conservative same-run/thread recovery. Production's
		// optional reader covers all statuses in one consistent transaction.
		thread, err := store.GetThread(ctx, record.Principal, record.ThreadID)
		if err != nil {
			return aguiFeedActivation{}, err
		}
		facts, err = aguistore.FactsFromFeedMessages(thread.Messages, record.ThreadID, id)
		if err != nil {
			return aguiFeedActivation{}, err
		}
		if record.LastSequence > 0 {
			journal, err := aguiRecoveryJournal(ctx, store, record)
			if err != nil {
				return aguiFeedActivation{}, fmt.Errorf("feed activation journal unavailable: %w", err)
			}
			for i, event := range journal {
				if fact, valid := agui.ReadFeedLifecycleFact(json.RawMessage(event), record.ThreadID, id, record.RunID); valid {
					fact.Journal = &agui.FeedLifecycleOrigin{RunID: record.RunID, Sequence: int64(i + 1)}
					facts = append(facts, fact)
				}
			}
		}
		facts = agui.ValidateFeedLifecycleSources(facts)
	}
	state := aguiFeedActivation{}
	for _, fact := range facts {
		state.observe(fact)
	}
	return state, nil
}
