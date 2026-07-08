# Startup Recovery Plan

## Goal

Recover interrupted interactive agent runs automatically on server startup, without human intervention. The recovery process:

- Runs once, ~10 seconds after startup, as a background goroutine
- Processes at most 10 root conversations concurrently (semaphore-bounded)
- Starts with the newest stale runs (most likely still relevant to users)
- Never touches scheduled runs (the scheduler watchdog owns those)
- Recurses depth-first from root conversations into child delegated conversations
- Resumes the ReAct loop after all pending tool calls are settled

---

## Design: Top-Down Recursive

### Why top-down

A child conversation only exists because a parent issued a `llm/agents:run` call. If the parent is stale, all its children are stale by causality. If the parent already succeeded, all its children completed too (the parent blocked synchronously waiting). Therefore all stale children trace back to a stale root. Scanning from roots and recursing depth-first covers every case with no orphan risk and no dependency graph needed.

### Core algorithm

```
RunOnce():
  runs = scan root stale interactive runs (newest first, limit 10)
  semaphore(10) — cap concurrent root recovery
  for each run:
    go recoverRun(ctx, run)

recoverRun(ctx, run):
  1. resolveRunAuth    → enriched ctx with valid tokens, or markInterrupted + skip
  2. settleToolPhase   → for each non-terminal tool_call in the turn:
       ordinary tool   → re-execute (or reuse existing result if response_payload_id set)
       delegated tool  → load linked_conversation_id from message
                       → recoverConversation(ctx, childConvID)   ← RECURSE
                       → synthesize RunOutput from child transcript
                       → write response_payload, finalize tool_call
  3. resumeReActLoop  → cleanup interim messages, rebuild QueryInput, call agent.Query()

recoverConversation(ctx, childConvID):
  load active run for conversation → recoverRun(ctx, run)
```

### What this eliminates vs bottom-up

| Removed complexity | Why not needed |
|---|---|
| `buildDependencyGraph()` | Recursion follows `linked_conversation_id` links naturally |
| Leaf-first ordering algorithm | Depth-first recursion gives bottom-up ordering automatically |
| `waiting_on_child` status | Parent never advances until child is fully recovered synchronously |
| Pre-scan of all child runs | Only visited when parent encounters a pending delegated tool call |

---

## Existing Infrastructure to Reuse

### `service/agent/watchdog.go`

Already implements stale run detection, auth restoration via `token.RestoreSecurityContext`, and resumption via `agent.Query()`. Recovery reuses the same patterns but:
- runs once at startup (not polling)
- handles tool-phase settlement before re-invoking the query
- recurses into children

### `pkg/agently/run/stale/run_stale.go`

`StaleRunsView` already has all fields needed:
- `Id`, `ConversationId`, `ConversationKind`, `AgentId`
- `SecurityContext`, `EffectiveUserId`, `AuthAuthority`, `AuthAudience`
- `ResumedFromRunId` (skip already-resumed runs)
- `LastHeartbeatAt` (stale threshold check)

### `pkg/agently/conversation/conversation.go`

`ToolCallView` already has:
- `MessageId`, `TurnId`, `OpId`, `ToolName`, `Status`
- `RequestPayloadId`, `ResponsePayloadId` (idempotency: skip if response already set)
- `RunId`, `Attempt`

`MessageView` has `LinkedConversationId` — the pointer from a `llm/agents:run` tool message to the child conversation.

### `sdk/handler.go`

Already launches the scheduler watchdog via:
```go
if cfg.schedulerOpts != nil && cfg.schedulerOpts.EnableWatchdog && cfg.schedulerSvc != nil {
    go cfg.schedulerSvc.StartWatchdog(ctx)
}
```
Recovery uses the same `go func()` pattern with a 10-second startup delay.

### `internal/auth/token` package

`token.RestoreSecurityContext(ctx, securityContextStr)` reconstructs auth context from the persisted `security_context` column. `token.Provider.EnsureTokens(ctx, key)` refreshes tokens if needed.

---

## Schema State

The `run` table already has:
- `conversation_kind` — `'interactive'` vs `'scheduled'`
- `conversation_id` → join to `conversation.conversation_parent_id` for root check
- `security_context` — serialized auth
- `effective_user_id`, `auth_authority`, `auth_audience`
- `resumed_from_run_id` — lineage tracking
- `last_heartbeat_at` — stale detection

The `tool_call` table already has:
- `message_id`, `turn_id`, `op_id`, `tool_name`, `status`
- `request_payload_id`, `response_payload_id` — for idempotent re-execution
- `run_id` — links back to the owning run

The `message` table already has:
- `linked_conversation_id` — child conversation created by `llm/agents:run`

**Schema additions needed:**

```sql
-- Add interrupted to run status (if not already permitted by CHECK)
-- Add composite index for root stale run scan
CREATE INDEX IF NOT EXISTS idx_run_recovery
    ON run(conversation_kind, status, last_heartbeat_at)
    WHERE conversation_kind = 'interactive';

-- MySQL equivalent
ALTER TABLE run ADD INDEX idx_run_recovery (conversation_kind, status, last_heartbeat_at);
```

---

## New Components

---

### 1. `service/agent/recovery.go` (new)

**Purpose:** One-shot startup recovery orchestrator. Runs once on startup; never touches scheduled runs.

```go
type Recovery struct {
    data          data.Service
    agent         *Service
    tokenProvider token.Provider
    tokenStore    svcauth.TokenStore     // for loadFreshToken
    mcpMgr        *mcpmgr.Manager
    concurrency   int                   // default 10
    startupDelay  time.Duration         // default 10s
    staleAfter    time.Duration         // default 2*watchdog_interval
}

type RecoveryOption func(*Recovery)

func WithRecoveryConcurrency(n int) RecoveryOption
func WithRecoveryStartupDelay(d time.Duration) RecoveryOption
func WithRecoveryStaleAfter(d time.Duration) RecoveryOption

func NewRecovery(
    data data.Service,
    agent *Service,
    tokenProvider token.Provider,
    tokenStore svcauth.TokenStore,
    mcpMgr *mcpmgr.Manager,
    opts ...RecoveryOption,
) *Recovery
```

**`RunOnce(ctx context.Context)`**

Called once from a goroutine with startup delay:
```go
func (r *Recovery) RunOnce(ctx context.Context) {
    time.Sleep(r.startupDelay)
    runs, err := r.scanRootStaleRuns(ctx)
    if err != nil {
        log.Printf("[recovery] scan: %v", err)
        return
    }
    // Sort newest first (most likely still relevant to users)
    sort.Slice(runs, func(i, j int) bool {
        return runs[i].CreatedAt.After(runs[j].CreatedAt)
    })
    // Cap at concurrency limit
    if len(runs) > r.concurrency {
        runs = runs[:r.concurrency]
    }
    sem := make(chan struct{}, r.concurrency)
    var wg sync.WaitGroup
    for _, run := range runs {
        sem <- struct{}{}
        wg.Add(1)
        go func(run *agrunstale.StaleRunsView) {
            defer wg.Done()
            defer func() { <-sem }()
            if err := r.recoverRun(ctx, run); err != nil {
                log.Printf("[recovery] run %s: %v", run.Id, err)
            }
        }(run)
    }
    wg.Wait()
}
```

**`scanRootStaleRuns(ctx) ([]*agrunstale.StaleRunsView, error)`**

Queries:
```sql
SELECT r.*
FROM run r
LEFT JOIN conversation c ON c.id = r.conversation_id
WHERE r.status = 'running'
  AND r.conversation_kind = 'interactive'
  AND (c.conversation_parent_id IS NULL OR c.conversation_parent_id = '')
  AND (r.last_heartbeat_at IS NULL OR r.last_heartbeat_at < ?)
  AND r.resumed_from_run_id IS NULL
ORDER BY r.created_at DESC
LIMIT ?
```

Uses the existing `StaleRunsInput` predicate mechanism with an added `ConversationKind` and parent filter. May require a new DQL-backed Datly component (`pkg/agently/run/interrupted/`) or a direct SQL query in the data service.

**`recoverRun(ctx, run *agrunstale.StaleRunsView) error`**

```go
func (r *Recovery) recoverRun(ctx context.Context, run *agrunstale.StaleRunsView) error {
    log.Printf("[recovery] recovering run %s conv=%v", run.Id, strVal(run.ConversationId))

    // 1. Resolve auth
    authCtx, err := r.resolveRunAuth(ctx, run)
    if err != nil {
        return r.markRunInterrupted(ctx, run.Id, fmt.Sprintf("auth failed: %v", err))
    }

    // 2. Settle all tool calls
    if err := r.settleToolPhase(authCtx, run); err != nil {
        return r.markRunInterrupted(ctx, run.Id, fmt.Sprintf("tool phase failed: %v", err))
    }

    // 3. Resume ReAct loop
    return r.resumeReActLoop(authCtx, run)
}
```

**`recoverConversation(ctx, childConvID string) error`**

```go
func (r *Recovery) recoverConversation(ctx context.Context, childConvID string) error {
    run, err := r.data.GetActiveRunByConversation(ctx, childConvID)
    if err != nil || run == nil {
        return fmt.Errorf("no active run for child conversation %s: %v", childConvID, err)
    }
    return r.recoverRun(ctx, run)
}
```

---

### 2. `service/agent/recovery_auth.go` (new)

**Purpose:** Auth context reconstruction. Reuses the same `security_context` restoration the watchdog already does, but adds token refresh and a hard failure path.

```go
func (r *Recovery) resolveRunAuth(ctx context.Context, run *agrunstale.StaleRunsView) (context.Context, error)
```

**Auth resolution order:**

1. If `run.SecurityContext` is set → `token.RestoreSecurityContext(ctx, *run.SecurityContext)` to get enriched context + `SecurityData`
2. If `sd.Subject != ""` → `r.tokenProvider.EnsureTokens(ctx, token.Key{Subject: sd.Subject, Provider: sd.Provider})` to refresh if near expiry
3. If no `SecurityContext` but `run.EffectiveUserId` set → try `r.tokenStore.Get(ctx, *run.EffectiveUserId, provider)` directly
4. If token expired and refresh fails → return error → caller marks run interrupted
5. If no auth context at all (anonymous run) → proceed with unauthenticated ctx (many tools don't need auth)

```go
func (r *Recovery) resolveRunAuth(ctx context.Context, run *agrunstale.StaleRunsView) (context.Context, error) {
    if run.SecurityContext == nil || strings.TrimSpace(*run.SecurityContext) == "" {
        // Anonymous or unauthenticated run — proceed without enrichment
        if run.EffectiveUserId != nil && strings.TrimSpace(*run.EffectiveUserId) != "" {
            ctx = svcauth.InjectUser(ctx, strings.TrimSpace(*run.EffectiveUserId))
        }
        return ctx, nil
    }
    enriched, sd, err := token.RestoreSecurityContext(ctx, *run.SecurityContext)
    if err != nil {
        return ctx, fmt.Errorf("restore security context: %w", err)
    }
    if sd != nil && r.tokenProvider != nil && strings.TrimSpace(sd.Subject) != "" {
        key := token.Key{Subject: sd.Subject, Provider: sd.Provider}
        if _, err := r.tokenProvider.EnsureTokens(enriched, key); err != nil {
            // Non-fatal: log and continue with existing tokens if any
            log.Printf("[recovery] token refresh for %s: %v", sd.Subject, err)
        }
    }
    return enriched, nil
}
```

---

### 3. `service/agent/recovery_tools.go` (new)

**Purpose:** Inspect and settle all pending tool calls in the interrupted turn. Delegated tools recurse into child conversations.

```go
type toolCallKind int

const (
    toolCallKindTerminal  toolCallKind = iota
    toolCallKindOrdinary                 // re-execute or reuse
    toolCallKindDelegated                // llm/agents:run
)

func (r *Recovery) settleToolPhase(ctx context.Context, run *agrunstale.StaleRunsView) error
```

**`settleToolPhase`** iterates all tool calls for the interrupted turn:

```go
func (r *Recovery) settleToolPhase(ctx context.Context, run *agrunstale.StaleRunsView) error {
    if run.TurnId == nil || strings.TrimSpace(*run.TurnId) == "" {
        return nil // no turn yet, nothing to settle
    }
    toolCalls, err := r.data.ListToolCallsByTurn(ctx, *run.TurnId)
    if err != nil {
        return fmt.Errorf("list tool calls: %w", err)
    }
    for _, tc := range toolCalls {
        kind := r.classifyToolCall(tc)
        switch kind {
        case toolCallKindTerminal:
            continue
        case toolCallKindOrdinary:
            if err := r.settleOrdinaryTool(ctx, tc); err != nil {
                log.Printf("[recovery] ordinary tool %s op=%s: %v", tc.ToolName, tc.OpId, err)
                _ = r.markToolCallFailed(ctx, tc, err.Error())
            }
        case toolCallKindDelegated:
            if err := r.settleDelegatedTool(ctx, tc); err != nil {
                log.Printf("[recovery] delegated tool %s op=%s: %v", tc.ToolName, tc.OpId, err)
                _ = r.markToolCallFailed(ctx, tc, err.Error())
            }
        }
    }
    return nil
}
```

**Classification logic:**

| Condition | Category | Action |
|---|---|---|
| `status` already terminal (`completed`, `failed`, `canceled`) | terminal | Skip |
| `tool_name` matches `llm/agents*`, status non-terminal | delegated | Recurse into child |
| `response_payload_id` set, status `running` | ordinary | Finalize status only (result exists) |
| `response_payload_id` nil, status `running`/`queued` | ordinary | Re-execute from `request_payload_id` |

```go
func (r *Recovery) classifyToolCall(tc *conv.ToolCallView) toolCallKind {
    s := strings.ToLower(strings.TrimSpace(tc.Status))
    if s == "completed" || s == "failed" || s == "canceled" {
        return toolCallKindTerminal
    }
    if strings.HasPrefix(strings.ToLower(tc.ToolName), "llm/agents") {
        return toolCallKindDelegated
    }
    return toolCallKindOrdinary
}
```

**`settleOrdinaryTool`:**

```go
func (r *Recovery) settleOrdinaryTool(ctx context.Context, tc *conv.ToolCallView) error {
    // Idempotent: if response already exists, just finalize status
    if tc.ResponsePayloadId != nil && strings.TrimSpace(*tc.ResponsePayloadId) != "" {
        return r.finalizeToolCallStatus(ctx, tc.MessageId, "completed", "")
    }
    // Re-execute: load original args from request_payload_id
    args, err := r.loadRequestPayload(ctx, tc)
    if err != nil {
        return fmt.Errorf("load request payload: %w", err)
    }
    result, execErr := r.agent.ExecuteTool(ctx, tc.ToolName, args)
    if execErr != nil {
        return r.markToolCallFailed(ctx, tc, execErr.Error())
    }
    return r.writeToolResult(ctx, tc, result)
}
```

**`settleDelegatedTool`:**

```go
func (r *Recovery) settleDelegatedTool(ctx context.Context, tc *conv.ToolCallView) error {
    // Get linked_conversation_id from the tool message
    msg, err := r.data.GetMessageByID(ctx, tc.MessageId)
    if err != nil || msg == nil {
        return fmt.Errorf("load tool message: %w", err)
    }
    if msg.LinkedConversationId == nil || strings.TrimSpace(*msg.LinkedConversationId) == "" {
        // Child conversation was never created — tool failed before launching child
        return r.markToolCallFailed(ctx, tc, "child conversation was never created")
    }
    childConvID := strings.TrimSpace(*msg.LinkedConversationId)

    // Check child state first — only recurse if child is stale/running
    childConv, err := r.data.GetConversation(ctx, childConvID)
    if err != nil || childConv == nil {
        return r.markToolCallFailed(ctx, tc, fmt.Sprintf("child conversation %s not found", childConvID))
    }
    childStatus := strings.ToLower(strings.TrimSpace(valueOrEmpty(childConv.Status)))
    if childStatus == "running" {
        // Child may be stale — recurse to recover it first
        if err := r.recoverConversation(ctx, childConvID); err != nil {
            log.Printf("[recovery] child conversation %s recovery failed: %v", childConvID, err)
        }
        // Re-read child status after recovery attempt
        childConv, err = r.data.GetConversation(ctx, childConvID)
        if err != nil || childConv == nil {
            return r.markToolCallFailed(ctx, tc, "child conversation lost after recovery")
        }
        childStatus = strings.ToLower(strings.TrimSpace(valueOrEmpty(childConv.Status)))
    }
    // Synthesize RunOutput from child transcript and project into parent
    output, err := r.synthesizeRunOutput(ctx, childConvID, childStatus)
    if err != nil {
        return fmt.Errorf("synthesize child output: %w", err)
    }
    return r.writeToolResult(ctx, tc, output)
}
```

**`synthesizeRunOutput`:**

Reads the child conversation transcript. The last assistant message is the answer. Conversation status maps to RunOutput status.

```go
func (r *Recovery) synthesizeRunOutput(ctx context.Context, childConvID, childStatus string) (string, error) {
    conv, err := r.data.GetConversation(ctx, childConvID)
    if err != nil || conv == nil {
        return "", fmt.Errorf("get child conversation: %w", err)
    }
    answer := ""
    for _, turn := range conv.Transcript {
        for _, msg := range turn.Messages {
            if strings.EqualFold(msg.Role, "assistant") && msg.Content != nil {
                if s := strings.TrimSpace(*msg.Content); s != "" {
                    answer = s
                }
            }
        }
    }
    out := map[string]interface{}{
        "answer":         answer,
        "status":         childStatus,
        "conversationId": childConvID,
    }
    data, _ := json.Marshal(out)
    return string(data), nil
}
```

---

### 4. `service/agent/recovery_resume.go` (new)

**Purpose:** Final step — clean up interim messages and re-enter the ReAct loop.

```go
func (r *Recovery) resumeReActLoop(ctx context.Context, run *agrunstale.StaleRunsView) error
```

```go
func (r *Recovery) resumeReActLoop(ctx context.Context, run *agrunstale.StaleRunsView) error {
    if run.ConversationId == nil || strings.TrimSpace(*run.ConversationId) == "" {
        return r.markRunInterrupted(ctx, run.Id, "no conversation to resume")
    }
    convID := strings.TrimSpace(*run.ConversationId)

    // Clean up interim assistant messages from the failed run to prevent duplication
    if err := r.cleanupInterimMessages(ctx, run); err != nil {
        log.Printf("[recovery] cleanup interim messages run=%s: %v", run.Id, err)
    }

    // Mark old run as interrupted — a new resumed run will be created by agent.Query()
    if err := r.markRunInterrupted(ctx, run.Id, "recovered at startup"); err != nil {
        return err
    }

    // Build QueryInput for resumption: empty Query continues the conversation
    agentID := ""
    if run.AgentId != nil {
        agentID = strings.TrimSpace(*run.AgentId)
    }
    input := &QueryInput{
        AgentID:                agentID,
        ConversationID:         convID,
        Query:                  "",    // continue existing conversation
        SkipInitialUserMessage: true,
    }
    out := &QueryOutput{}
    log.Printf("[recovery] resuming conv=%s agent=%s", convID, agentID)
    if err := r.agent.Query(ctx, input, out); err != nil {
        log.Printf("[recovery] resume failed conv=%s: %v", convID, err)
        return err
    }
    log.Printf("[recovery] resumed conv=%s", convID)
    return nil
}
```

**`cleanupInterimMessages`:**

Marks assistant messages with `interim=1` from the interrupted run as superseded so they don't appear in the transcript when the ReAct loop resumes.

```go
func (r *Recovery) cleanupInterimMessages(ctx context.Context, run *agrunstale.StaleRunsView) error {
    if run.TurnId == nil {
        return nil
    }
    // Patch interim=1 messages in this turn to interim=0 or delete them
    // using existing conversation client PatchMessage or similar
    return r.agent.conversation.DeleteInterimMessages(ctx, *run.TurnId)
}
```

**`markRunInterrupted`:**

```go
func (r *Recovery) markRunInterrupted(ctx context.Context, runID, reason string) error {
    upd := &agrunwrite.MutableRunView{}
    upd.SetId(runID)
    upd.SetStatus("interrupted")
    upd.SetErrorMessage(reason)
    upd.SetCompletedAt(time.Now())
    _, err := r.data.PatchRuns(ctx, []*agrunwrite.MutableRunView{upd})
    return err
}
```

---

## Existing Files to Extend

---

### 5. `app/store/data/service.go`

Add to the `Service` interface and `datlyService` implementation:

| New method | Purpose | SQL |
|---|---|---|
| `ListRootStaleRuns(ctx, threshold, limit)` | Root stale interactive runs only | `WHERE status='running' AND conversation_kind='interactive' AND (conv.conversation_parent_id IS NULL OR conv.conversation_parent_id='') AND (last_heartbeat_at IS NULL OR last_heartbeat_at < ?) AND resumed_from_run_id IS NULL ORDER BY created_at DESC LIMIT ?` |
| `ListToolCallsByTurn(ctx, turnID)` | All tool calls for a turn | `SELECT * FROM tool_call WHERE turn_id = ?` |
| `GetMessageByID(ctx, messageID)` | Message + linked_conversation_id | `SELECT * FROM message WHERE id = ?` |
| `GetActiveRunByConversation(ctx, convID)` | Active run for child recursion | `SELECT * FROM run WHERE conversation_id = ? AND status = 'running' ORDER BY created_at DESC LIMIT 1` |
| `GetConversation(ctx, convID)` | Already exists — verify it returns `Status` and `Transcript` |

---

### 6. New Datly query packages

These are DQL-generated (do not hand-edit; generate via `datly gen`):

| Package | SQL filter | Purpose |
|---|---|---|
| `pkg/agently/run/interrupted/` | `status='running' AND conversation_kind='interactive' AND (conversation_parent_id IS NULL) AND (last_heartbeat_at IS NULL OR last_heartbeat_at < ?) AND resumed_from_run_id IS NULL` | Root stale run scan |
| `pkg/agently/toolcall/read/byturn/` | `SELECT * FROM tool_call WHERE turn_id = ?` | Tool phase settlement |
| `pkg/agently/message/byid/` | `SELECT * FROM message WHERE id = ?` | Linked conversation lookup (may already exist) |

---

### 7. `sdk/handler.go`

Add `WithRecovery` option mirroring `WithScheduler`:

```go
type RecoveryOptions struct {
    Enabled      bool
    StartupDelay time.Duration // default 10s
    Concurrency  int           // default 10
}

func WithRecovery(
    data data.Service,
    agent *agentsvc.Service,
    tokenProvider token.Provider,
    tokenStore svcauth.TokenStore,
    mcpMgr *mcpmgr.Manager,
    opts *RecoveryOptions,
) HandlerOption {
    return func(c *handlerConfig) {
        c.recoveryData          = data
        c.recoveryAgent         = agent
        c.recoveryTokenProvider = tokenProvider
        c.recoveryTokenStore    = tokenStore
        c.recoveryMCPMgr        = mcpMgr
        c.recoveryOpts          = opts
    }
}
```

In `NewHandlerWithContext()`, after existing watchdog wiring:

```go
if cfg.recoveryOpts != nil && cfg.recoveryOpts.Enabled {
    delay := cfg.recoveryOpts.StartupDelay
    if delay == 0 {
        delay = 10 * time.Second
    }
    concurrency := cfg.recoveryOpts.Concurrency
    if concurrency == 0 {
        concurrency = 10
    }
    recovery := agentsvc.NewRecovery(
        cfg.recoveryData,
        cfg.recoveryAgent,
        cfg.recoveryTokenProvider,
        cfg.recoveryTokenStore,
        cfg.recoveryMCPMgr,
        agentsvc.WithRecoveryStartupDelay(delay),
        agentsvc.WithRecoveryConcurrency(concurrency),
    )
    go recovery.RunOnce(ctx)
}
```

---

### 8. `service/agent/watchdog.go`

Add guard clauses to `handleStaleRun` to prevent double-processing:

```go
func (w *Watchdog) handleStaleRun(ctx context.Context, run *agrunstale.StaleRunsView) error {
    // Skip scheduled runs — scheduler watchdog owns those
    if strings.EqualFold(run.ConversationKind, "scheduled") {
        return nil
    }
    // Skip runs already being recovered or resumed
    if run.ResumedFromRunId != nil && strings.TrimSpace(*run.ResumedFromRunId) != "" {
        return nil
    }
    // ... existing logic
}
```

---

### 9. Schema additions

**`script/schema.ddl`** and **`script/mysql/schema.ddl`**:

```sql
-- Add interrupted status support to run (verify existing CHECK constraint allows it)
-- Most SQLite schemas don't enforce CHECK at runtime, but document it:

-- Add index for recovery root scan
CREATE INDEX IF NOT EXISTS idx_run_recovery
    ON run(conversation_kind, status, last_heartbeat_at, resumed_from_run_id);

-- MySQL equivalent
-- ALTER TABLE run ADD INDEX idx_run_recovery
--     (conversation_kind, status, last_heartbeat_at, resumed_from_run_id);
```

---

### 10. `agently/serve.go`

Wire recovery after the SDK handler is built:

```go
// In Serve(), after handler creation:
sdkOpts = append(sdkOpts,
    sdk.WithRecovery(
        rt.Data,
        rt.Agent,
        rt.TokenProvider,
        authRuntime.TokenStore,
        rt.MCPManager,
        &sdk.RecoveryOptions{
            Enabled:      true,
            StartupDelay: 10 * time.Second,
            Concurrency:  10,
        },
    ),
)
```

---

## Key Invariants

| # | Invariant | Enforcement |
|---|---|---|
| 1 | Never touch scheduled runs | `conversation_kind != 'scheduled'` filter in scan query + guard in watchdog |
| 2 | Root conversations only at scan time | `conversation_parent_id IS NULL` in root scan; children found via recursion |
| 3 | Never resume terminal runs | `status = 'running'` filter in scan query |
| 4 | Skip already-resumed runs | `resumed_from_run_id IS NULL` in scan query + watchdog guard |
| 5 | No LLM call until all tools terminal | `settleToolPhase` completes (all non-terminal tools settled) before `resumeReActLoop` |
| 6 | Never re-execute delegated calls directly | `settleDelegatedTool` recurses into child via `recoverConversation`, never calls `llm/agents:run` |
| 7 | Auth fail = stop | `resolveRunAuth` returns error → `markRunInterrupted`, skip |
| 8 | Idempotent re-execution | Check `response_payload_id != nil` before re-executing any ordinary tool |
| 9 | Cap concurrent recoveries | Semaphore of size 10 in `RunOnce`, newest-first ordering |

---

## Phased Delivery

### Phase 1 — Ordinary tool recovery

**Files:** `recovery.go`, `recovery_auth.go`, `recovery_tools.go` (ordinary path only), `recovery_resume.go`, data service extensions, schema index, `sdk/handler.go`, `agently/serve.go`

**Behavior:** Interrupted interactive turns with only ordinary (non-delegated) tools resume cleanly. Delegated tool calls detected and logged; parent marked `interrupted` with reason `"unresolved delegated tool calls"`.

**Deliverable:** End-to-end recovery for the majority of interrupted runs (those without `llm/agents:run` in the interrupted turn).

---

### Phase 2 — Delegated recovery + recursion

**Files:** `recovery_tools.go` (delegated path + `synthesizeRunOutput`), `recoverConversation` in `recovery.go`

**Behavior:** `settleDelegatedTool` recurses into child conversations. Children recovered first (depth-first), then parent tool calls resolved from child transcript, then parent ReAct loop resumes.

**Deliverable:** Full recovery of multi-agent delegation chains.

---

### Phase 3 — Observability

**SSE events:** `run_recovering`, `run_resumed`, `run_recovery_failed`

**Metrics:** Recovery success/failure rate, tool re-execution count, auth failure count, average recovery duration

---

## File Summary

| File | Status | Purpose |
|---|---|---|
| `service/agent/recovery.go` | **New** | `Recovery` struct, `RunOnce`, `recoverRun`, `recoverConversation`, `scanRootStaleRuns` |
| `service/agent/recovery_auth.go` | **New** | `resolveRunAuth`, `loadFreshToken`, `buildAuthContext` |
| `service/agent/recovery_tools.go` | **New** | `settleToolPhase`, `classifyToolCall`, `settleOrdinaryTool`, `settleDelegatedTool`, `synthesizeRunOutput`, `writeToolResult` |
| `service/agent/recovery_resume.go` | **New** | `resumeReActLoop`, `cleanupInterimMessages`, `buildResumeQueryInput`, `markRunInterrupted` |
| `service/agent/watchdog.go` | **Extend** | Add `conversation_kind` and `resumed_from_run_id` guards to `handleStaleRun` |
| `app/store/data/service.go` | **Extend** | `ListRootStaleRuns`, `ListToolCallsByTurn`, `GetMessageByID`, `GetActiveRunByConversation` |
| `pkg/agently/run/interrupted/` | **New (DQL)** | Root stale interactive run query |
| `pkg/agently/toolcall/read/byturn/` | **New (DQL)** | Tool calls by turn query |
| `sdk/handler.go` | **Extend** | `WithRecovery` option, launch in `NewHandlerWithContext` |
| `script/schema.ddl` | **Extend** | Recovery index on `run` table |
| `script/mysql/schema.ddl` | **Extend** | Same index for MySQL |
| `agently/serve.go` | **Extend** | Wire `WithRecovery` after handler creation |
