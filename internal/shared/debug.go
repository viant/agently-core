package shared

import "github.com/viant/agently-core/internal/logx"

// DebugAttachmentsEnabled reports whether attachment-related debug logging is enabled.
// It is intentionally coarse-grained and should never be used to print secrets
// or raw attachment bytes.
func DebugAttachmentsEnabled() bool {
	return logx.EnabledFor("AGENTLY_DEBUG_ATTACHMENTS", "AGENTLY_DEBUG")
}
