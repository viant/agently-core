package sdk

import (
	"encoding/json"

	"github.com/viant/agently-core/runtime/aguistate"
)

// A snapshot echo publishes observed state; a different snapshot or a delta is
// an intentional write. Admission/message revisions do not imply state conflict.
func aguiIntentionalStateWrite(events []json.RawMessage, baseline json.RawMessage) (bool, error) {
	for _, raw := range events {
		var event struct {
			Type     string
			Snapshot json.RawMessage
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return false, err
		}
		if event.Type == "STATE_DELTA" {
			return true, nil
		}
		if event.Type == "STATE_SNAPSHOT" {
			same, err := aguistate.EqualJSON(event.Snapshot, baseline)
			if err != nil || !same {
				return !same, err
			}
		}
	}
	return false, nil
}

func aguiEchoSnapshot(raw json.RawMessage, state json.RawMessage) (json.RawMessage, error) {
	var event map[string]json.RawMessage
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, err
	}
	var kind string
	if err := json.Unmarshal(event["type"], &kind); err != nil {
		return nil, err
	}
	if kind != "STATE_SNAPSHOT" {
		return raw, nil
	}
	event["snapshot"] = state
	return json.Marshal(event)
}
