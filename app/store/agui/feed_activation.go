package agui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	wire "github.com/viant/agently-core/protocol/agui"
)

// FeedActivationJournalReader is optional so custom stores retain their existing
// contract. Production returns only complete, consistently scoped read snapshots.
type FeedActivationJournalReader interface {
	ReadFeedActivationFacts(context.Context, string, string, string) ([]wire.FeedLifecycleFact, error)
}

func (s *ComponentStore) ReadFeedActivationFacts(ctx context.Context, principal, threadID, feedID string) ([]wire.FeedLifecycleFact, error) {
	if principal == "" || threadID == "" || feedID == "" {
		return nil, fmt.Errorf("feed lifecycle read requires owned thread and feed")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := s.invoke(ctx, &Request{Operation: "feedfacts", Principal: principal, ThreadID: threadID, FeedID: feedID})
	if err != nil {
		return nil, err
	}
	if output.FeedFacts == nil {
		return nil, fmt.Errorf("feed lifecycle snapshot was not completed")
	}
	return output.FeedFacts, nil
}

// FactsFromFeedMessages retains native message facts without treating their array
// order or current projection timestamp as lifecycle order.
func FactsFromFeedMessages(raw json.RawMessage, threadID, feedID string) ([]wire.FeedLifecycleFact, error) {
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, err
	}
	facts := []wire.FeedLifecycleFact{}
	for _, message := range messages {
		if fact, valid := wire.ReadFeedLifecycleFact(message, threadID, feedID); valid {
			facts = append(facts, fact)
		}
	}
	return facts, nil
}
