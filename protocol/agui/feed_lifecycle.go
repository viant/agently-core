package agui

import (
	"encoding/json"
	"github.com/viant/agently-core/protocol/agui/extensions"
	"time"
)

// FeedLifecycleOrigin identifies an original journal fact, never a refresh's
// position. It becomes ordering authority only after scoped journal validation.
type FeedLifecycleOrigin struct {
	RunID    string `json:"runId"`
	Sequence int64  `json:"sequence"`
}

type FeedLifecycleFact struct {
	Active    bool
	At        time.Time
	Origin    *FeedLifecycleOrigin
	Journal   *FeedLifecycleOrigin
	Inherited bool
}

// ReadFeedLifecycleFact accepts only explicit version-one lifecycle booleans.
// A retained feed payload or event emission timestamp is not lifecycle evidence.
func ReadFeedLifecycleFact(raw json.RawMessage, threadID, feedID string, journalRunID ...string) (FeedLifecycleFact, bool) {
	var event struct {
		Type, Role, ActivityType string
		ThreadID, RunID          string
		Content                  struct {
			Version, FeedID, ActivationAt string
			Active                        *bool
			ActivationKnown               *bool
			ActivationSource              json.RawMessage
			Feed                          *struct{ FeedID string }
			ActivationFact                json.RawMessage
		}
		Metadata struct {
			Agently struct {
				Presentation struct{ ConversationID, CreatedAt string }
			}
		}
	}
	if json.Unmarshal(raw, &event) != nil || event.ActivityType != "agently.feed" || event.Content.Version != "1" || (event.Type != "ACTIVITY_SNAPSHOT" && event.Role != "activity") {
		return FeedLifecycleFact{}, false
	}
	if event.ThreadID != "" && event.ThreadID != threadID {
		return FeedLifecycleFact{}, false
	}
	if event.RunID != "" && len(journalRunID) > 0 && event.RunID != journalRunID[0] {
		return FeedLifecycleFact{}, false
	}
	if scope := event.Metadata.Agently.Presentation.ConversationID; scope != "" && scope != threadID {
		return FeedLifecycleFact{}, false
	}
	id := event.Content.FeedID
	if event.Content.Feed != nil {
		if id != "" && id != event.Content.Feed.FeedID {
			return FeedLifecycleFact{}, false
		}
		id = event.Content.Feed.FeedID
	}
	if id == "" || id != feedID {
		return FeedLifecycleFact{}, false
	}
	if len(event.Content.ActivationFact) > 0 {
		if extensions.ValidatePresentation("FeedActivationFact", event.Content.ActivationFact) != nil {
			return FeedLifecycleFact{}, false
		}
		var content map[string]json.RawMessage
		if json.Unmarshal(event.Content.ActivationFact, &content) != nil || len(content["activationFact"]) > 0 {
			return FeedLifecycleFact{}, false
		}
		wrapped, _ := json.Marshal(map[string]any{"type": "ACTIVITY_SNAPSHOT", "activityType": "agently.feed", "content": content})
		return ReadFeedLifecycleFact(wrapped, threadID, feedID)
	}
	if event.Content.Active == nil || (event.Content.ActivationKnown != nil && !*event.Content.ActivationKnown) {
		return FeedLifecycleFact{}, false
	}
	var source *FeedLifecycleOrigin
	if len(event.Content.ActivationSource) > 0 {
		if extensions.ValidatePresentation("FeedActivationSource", event.Content.ActivationSource) != nil || json.Unmarshal(event.Content.ActivationSource, &source) != nil {
			return FeedLifecycleFact{}, false
		}
	}
	stamp := event.Content.ActivationAt
	inherited := event.Content.Feed != nil || event.Content.ActivationKnown != nil
	if stamp == "" && !inherited {
		stamp = event.Metadata.Agently.Presentation.CreatedAt
	}
	at, _ := time.Parse(time.RFC3339Nano, stamp)
	return FeedLifecycleFact{Active: *event.Content.Active, At: at, Origin: source, Inherited: inherited}, true
}

// ValidateFeedLifecycleSources is called only after a complete principal/thread
// snapshot. A claimed source must match an actual original fact in that snapshot.
// Foreign/missing sources lose sequence authority; their timestamps are unchanged.
func ValidateFeedLifecycleSources(facts []FeedLifecycleFact) []FeedLifecycleFact {
	originals := map[FeedLifecycleOrigin]FeedLifecycleFact{}
	for _, fact := range facts {
		if fact.Journal == nil || fact.Journal.RunID == "" || fact.Journal.Sequence < 1 {
			continue
		}
		if !fact.Inherited && (fact.Origin == nil || *fact.Origin == *fact.Journal) {
			originals[*fact.Journal] = fact
		}
	}
	for i := range facts {
		fact := &facts[i]
		claimed := fact.Origin
		if claimed == nil && !fact.Inherited {
			claimed = fact.Journal
		}
		fact.Origin = nil
		if claimed != nil {
			original, found := originals[*claimed]
			if found && original.Active == fact.Active && original.At.Equal(fact.At) {
				copy := *claimed
				fact.Origin = &copy
			}
		}
	}
	return facts
}
