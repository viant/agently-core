package manage

import (
	"fmt"

	store "github.com/viant/agently-core/app/store/agui"
	feedread "github.com/viant/agently-core/internal/datly/agui/feed_journal/read"
	wire "github.com/viant/agently-core/protocol/agui"
)

const feedJournalScanBound = 16384

// feedFacts shares one managed serializable transaction across the thread read
// and every keyset page. Enumeration is completeness, never lifecycle order.
func (tx *operation) feedFacts(input *store.Request, output *store.Response) error {
	if input.FeedID == "" {
		return fmt.Errorf("feed lifecycle identity is required")
	}
	thread, err := tx.thread()
	if err != nil {
		return err
	}
	if thread == nil {
		return store.ErrNotFound
	}
	if str(thread.Principal) != tx.principal || str(thread.ThreadId) != tx.threadID {
		return fmt.Errorf("feed lifecycle thread scope is inconsistent")
	}
	facts, err := store.FactsFromFeedMessages(thread.MessagesJson, tx.threadID, input.FeedID)
	if err != nil {
		return err
	}
	afterKey, afterSequence, scanned := "", int64(0), 0
	for {
		if err := tx.ctx.Err(); err != nil {
			return err
		}
		query := &feedread.Input{}
		query.SetPrincipal(tx.principal)
		query.SetThreadKey(store.ThreadKey(tx.threadID))
		query.SetAfterKey(afterKey)
		query.SetAfterSequence(afterSequence)
		value, err := tx.call("feed_journal/read", "reader", "GET", query)
		if err != nil {
			return err
		}
		page, ok := value.(*feedread.Output)
		if !ok || page == nil {
			return fmt.Errorf("feed journal reader returned %T", value)
		}
		for _, row := range page.Data {
			if row == nil || str(row.Principal) != tx.principal || str(row.ThreadId) != tx.threadID || str(row.RunId) == "" || str(row.RunKey) == "" || row.Sequence < 1 || row.Sequence > 9007199254740991 || str(row.RunKey) < afterKey || (str(row.RunKey) == afterKey && row.Sequence <= afterSequence) {
				return fmt.Errorf("feed journal identity or keyset is inconsistent")
			}
			if scanned == feedJournalScanBound {
				return fmt.Errorf("feed lifecycle snapshot exceeds complete-scan bound")
			}
			scanned++
			afterKey, afterSequence = str(row.RunKey), row.Sequence
			if fact, valid := wire.ReadFeedLifecycleFact(row.EventJson, tx.threadID, input.FeedID, str(row.RunId)); valid {
				fact.Journal = &wire.FeedLifecycleOrigin{RunID: str(row.RunId), Sequence: row.Sequence}
				facts = append(facts, fact)
			}
		}
		if len(page.Data) < 256 {
			output.FeedFacts = wire.ValidateFeedLifecycleSources(facts)
			return nil
		}
	}
}
