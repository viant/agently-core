// Package playbook is the preferred public profile model. The type aliases
// retain exact wire compatibility while existing intent consumers migrate.
package playbook

import legacy "github.com/viant/agently-core/protocol/intent"

type Profile = legacy.Profile
type Message = legacy.Message
type KnowledgeMatch = legacy.KnowledgeMatch
type Execution = legacy.Execution
type MCPSource = legacy.MCPSource
type Expansion = legacy.Expansion
type EvidenceContract = legacy.EvidenceContract
type RenderOptions = legacy.RenderOptions
type MCPManager = legacy.MCPManager
