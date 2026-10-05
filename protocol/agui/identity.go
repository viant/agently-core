package agui

import (
	"fmt"
	"net/url"
)

// ProtocolToolCallID scopes a native op id to its stable logical turn. A resumed
// turn keeps the same alias even when its external protocol run id changes.
func ProtocolToolCallID(nativeTurnID, nativeOpID string) string {
	if nativeTurnID == "" || nativeOpID == "" {
		return nativeOpID
	}
	return "agui.tool/" + url.PathEscape(nativeTurnID) + "/" + url.PathEscape(nativeOpID)
}

// SetNativeIdentity enables production aliases before the run starts. Prototype
// callers retain raw ids unless they explicitly opt in.
func (t *Translator) SetNativeIdentity(nativeTurnID string) error {
	if nativeTurnID == "" {
		return fmt.Errorf("native turn identity is required")
	}
	if t.started && t.nativeTurnID != nativeTurnID {
		return fmt.Errorf("native identity must be set before run start")
	}
	t.nativeTurnID = nativeTurnID
	return nil
}
func (t *Translator) toolID(turnID, nativeID string) string {
	if t.nativeTurnID == "" {
		return nativeID
	}
	if turnID == "" {
		turnID = t.nativeTurnID
	}
	return ProtocolToolCallID(turnID, nativeID)
}
