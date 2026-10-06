package sdk

import (
	"encoding/json"
	"strings"

	"github.com/viant/agently-core/runtime/aguistate"
)

// Reconstruct the raw-offset domain from native authoring checkpoints while
// keeping the journal's already-visible prefix. Without a raw rich checkpoint,
// defer deltas until an authoritative snapshot; never treat an offset gap as
// permission to publish unprojected authoring bytes.
func (p *aguiPresentation) prefill(conversationID, turnID, owner string, projected, native []aguistate.Object) {
	if p.messages == nil {
		p.messages = map[string]*aguiPresentationState{}
	}
	rawByID := map[string]string{}
	for _, message := range native {
		if message["role"] == "assistant" {
			if raw, ok := message["content"].(string); ok {
				id, _ := message["id"].(string)
				rawByID[id] = raw
			}
		}
	}
	for id, raw := range rawByID {
		p.messages[conversationID+"\x00"+turnID+"\x00"+id] = &aguiPresentationState{raw: raw}
	}
	for _, message := range projected {
		if message["role"] != "assistant" {
			continue
		}
		tag, _ := message["subagentRunId"].(string)
		if tag != owner {
			continue
		}
		visible, ok := message["content"].(string)
		if !ok {
			continue
		}
		id, _ := message["id"].(string)
		state := &aguiPresentationState{raw: visible, visible: visible}
		if raw, known := rawByID[id]; known {
			state.raw = raw
			if rendered := normalizeRenderedContent(raw, false); rendered != nil {
				// Only dedup an activity that already exists in the accepted projection.
				for _, activity := range projected {
					if activity["id"] == id+"/activity" {
						encoded, _ := json.Marshal(rendered)
						state.fingerprint = string(encoded)
						break
					}
				}
			}
		} else if strings.Contains(visible, aguiInteractiveFallback) {
			state.unresolved = true
		}
		p.messages[conversationID+"\x00"+turnID+"\x00"+id] = state
	}
}
