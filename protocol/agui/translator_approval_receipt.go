package agui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ReconcileApprovalReceipt is the native inspector's restricted publication
// seam. It permits validated authoritative snapshots and the receipt for one
// already-known tool bound to this native turn; generic producers remain unable
// to forge server-owned presentation or invocation metadata.
func (t *Translator) ReconcileApprovalReceipt(nativeTurnID, toolCallID string, snapshot, result *WireEvent) ([]Event, error) {
	if t.done || nativeTurnID == "" || t.nativeTurnID != nativeTurnID || t.tools[toolCallID] == nil {
		return nil, fmt.Errorf("approval receipt requires the registered native turn and tool")
	}
	if !strings.HasPrefix(toolCallID, "agui.tool/"+url.PathEscape(nativeTurnID)+"/") {
		return nil, fmt.Errorf("approval receipt tool belongs to another native turn")
	}
	if snapshot == nil || result == nil {
		return nil, fmt.Errorf("approval receipt events are required")
	}
	snapshotRaw, err := EncodeEvent(snapshot)
	if err != nil {
		return nil, err
	}
	resultRaw, err := EncodeEvent(result)
	if err != nil {
		return nil, err
	}
	var descriptorFields struct {
		Type string `json:"type"`
	}
	var receiptFields struct {
		Type       string `json:"type"`
		ToolCallID string `json:"toolCallId"`
	}
	if json.Unmarshal(snapshotRaw, &descriptorFields) != nil || json.Unmarshal(resultRaw, &receiptFields) != nil || descriptorFields.Type != "MESSAGES_SNAPSHOT" || receiptFields.Type != "TOOL_CALL_RESULT" || receiptFields.ToolCallID != toolCallID {
		return nil, fmt.Errorf("approval receipt event identity mismatch")
	}
	descriptor, err := StandardEvent(snapshotRaw)
	if err != nil {
		return nil, err
	}
	receipt, err := StandardEvent(resultRaw)
	if err != nil {
		return nil, err
	}
	events := append(t.Start(), descriptor, receipt)
	t.capture(events)
	if t.historyErr != nil {
		return nil, t.historyErr
	}
	return events, nil
}
