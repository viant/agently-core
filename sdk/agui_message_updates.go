package sdk

import (
	"encoding/json"
	"fmt"
	"reflect"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/runtime/aguistate"
)

// Merge only identities changed by this producer. Inherited messages are not
// writes. The caller persists the merged document and journal in the existing
// Datly transaction, guarded by the fresh thread revision.
func aguiChangedMessages(baseline, local, latest []aguistate.Object) ([]aguistate.Object, error) {
	index := func(messages []aguistate.Object) map[string]aguistate.Object {
		result := map[string]aguistate.Object{}
		for _, message := range messages {
			id, _ := message["id"].(string)
			result[id] = message
		}
		return result
	}
	old, current := index(baseline), index(latest)
	changed := make([]aguistate.Object, 0)
	for _, message := range local {
		id, _ := message["id"].(string)
		if reflect.DeepEqual(old[id], message) {
			continue
		}
		if current[id] != nil && !reflect.DeepEqual(current[id], old[id]) && !reflect.DeepEqual(current[id], message) {
			return nil, fmt.Errorf("%w: message %q changed concurrently", aguistore.ErrConflict, id)
		}
		changed = append(changed, message)
	}
	return mergeAGUIMessages(latest, changed), nil
}
func aguiCanonicalSnapshot(event json.RawMessage, messages []aguistate.Object) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(event, &fields); err != nil {
		return nil, err
	}
	var kind string
	_ = json.Unmarshal(fields["type"], &kind)
	if kind != "MESSAGES_SNAPSHOT" {
		return event, nil
	}
	fields["messages"] = rawAGUI(messages)
	return json.Marshal(fields)
}
