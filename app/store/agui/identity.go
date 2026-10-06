package agui

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

func identityKey(kind string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(kind))
	var size [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(size[:], uint64(len([]byte(part))))
		h.Write(size[:])
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func ThreadKey(threadID string) string { return identityKey("agui.thread.v1", threadID) }
func RunKey(principal, conversationID, runID string) string {
	return identityKey("agui.run.v1", principal, conversationID, runID)
}
func SourceKey(principal, conversationID, messageID string) string {
	return identityKey("agui.source.v1", principal, conversationID, messageID)
}
func InitialTurnKey(principal, conversationID, turnID string) string {
	return identityKey("agui.turn.v1", principal, conversationID, turnID)
}
