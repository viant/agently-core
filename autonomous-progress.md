# Autonomous Progress

## Current status

Implemented durable conversation-level goal persistence and the first
conversation-scoped `system/goal` tool surface.

## Landed

- persisted `goal` table with one-goal-per-conversation constraint
- Datly read / patch / delete components for goals
- `system/goal:get`
- `system/goal:create`
- `system/goal:update`
  - restricted to `complete` / `blocked`
  - requires durable `reason`
- `system/goal:pause`
- `system/goal:resume`
- `system/goal:clear`
- `status_reason` persistence and projection
- `service/goal` runtime bridge for post-turn usage accounting and controller
  evaluation
- agent-side autonomous continuation enqueue hook using the existing queued-turn
  machinery
- queued turn metadata now propagates `origin`, `goalId`, and `statusReason`
  through turn persistence, canonical state, and web queue previews
- goal lifecycle stream events now exist:
  - `goal.updated`
  - `goal.cleared`
  - `goal.controller_scheduled`
  - web + SDK stream trackers now consume them as live goal-feed updates
  - iOS + Android app runtimes now consume the streamed goal feed into
    visible `activeGoal` state too
  - `goal.controller_scheduled` now carries `mode=queue|wakeup`, and delayed
    wakeups include `wakeAt`
  - web goal summary/feed surfaces now render the delayed resume state directly
  - iOS and Android goal summary cards now render the delayed resume state
    directly from streamed goal feed metadata
  - iOS now reads that delayed-resume metadata from the live stream snapshot,
    matching Android and web live-update behavior
  - normal goal reads now also project pending delayed wakeups durably through
    `goal.controllerSchedule`, not only via live stream patches
- scheduler-backed delayed wakeups now exist for autonomous goals:
  - controller spec accepts optional `wakeDelaySeconds`
  - workspace config now enforces `features.wakeups.enabled`,
    `maxGlobalWakeupsPerHour`, `maxConversationWakeups`,
    `maxGoalWakeups`, `minWakeDelaySeconds`, and `maxWakeDelaySeconds`
  - controller can emit delayed wakeup instead of immediate queue continuation
  - hidden internal scheduler rows persist delayed wake state per goal /
    conversation
  - public scheduler get/delete/run-now/upsert paths now keep those internal
    wakeup rows hidden and reserved
  - wake rows resume the same conversation instead of creating a new scheduled
    conversation
  - when wakeups are disabled by workspace policy, autonomous continuation now
    falls back to immediate queue continuation
  - scheduler-side wakeup budget checks now reject over-budget delayed wakeups
    and trigger the same immediate-queue fallback
  - normal non-scheduled conversation activity cancels pending wakeups
  - controller-owned goal deactivation and manual goal mutations now cancel
    pending wakeups too
  - `system/goal` tool mutations cancel pending wakeups too
  - wake execution re-checks goal-active + idle predicates before running
  - focused scheduler tests now prove the delayed wakeup query targets the same
    conversation and carries the hidden goal wakeup context
  - focused scheduler execution tests now prove internal wakeups persist the
    resumed conversation id on the scheduler run row too
  - focused scheduler RunDue tests now prove hidden goal wakeup rows flow
    through the real due-run path and trigger same-conversation wakeup queries
  - focused SDK + `system/goal` tests now prove normal goal reads expose
    pending wakeup metadata through `controllerSchedule`
  - the real scheduler RunDue wakeup flow now also proves the pending wakeup
    lifecycle end to end at the scheduler boundary:
    future wakeup visible before execution, same-conversation resume when due,
    and pending wakeup cleared after the run starts
  - the public HTTP conversation goal API now has the same proof shape:
    `GET /v1/conversations/{id}/goal` exposes pending wakeup state before the
    run, and the same endpoint clears `controllerSchedule` after the real
    scheduler resume path consumes that wakeup
- default bundle wiring for bootstrap `coder` and `chatter`
- default workspace now also ships an explicit `autonomous` umbrella tool
  bundle over `system/goal:*` and `system/async:*`
- default workspace bootstrap config now enables the `system/goal` internal MCP
  service
- web/mobile SDK direct tool execution can now target conversation-scoped goal
  tools via optional `conversationId`
- public conversation goal API surface:
  - `GET /v1/conversations/{id}/goal`
  - `POST /v1/conversations/{id}/goal`
  - `PATCH /v1/conversations/{id}/goal`
  - `DELETE /v1/conversations/{id}/goal`
- SDK goal client methods added across:
  - Go embedded/http SDK
  - TypeScript SDK
  - iOS SDK
  - Android SDK
- web goal surface:
  - dedicated goal summary strip is visible in the web chat shell when
    `capabilities.goals=true`
  - active conversation now refreshes a local `goal` tool feed from the
    conversation goal API
  - goal feed renders visible goal actions (`Create`, `Edit`, `Pause`,
    `Resume`, `Clear`)
  - `/goal` composer activation is gated by workspace goal capability
  - goal feed can opt into Forge-container rendering through
    `ui.renderMode: forge` with feed-native goal handlers in `chatService`
  - default workspace now ships `feeds/goal.yaml`, so the goal feed schema can
    be served by the backend feed registry instead of living only in UI code
  - design pass improved hierarchy and control affordance:
    - semantic status pills
    - visible token/time usage
    - budget progress bars
    - quick `Pause` / `Resume` in the strip
    - confirm-before-clear in the web panel
  - dedicated goal-drafting thin now exists for no-goal state:
    - preset autonomous-goal header styles
    - multiline template authoring
    - launched from the no-goal summary strip
- native mobile goal surface:
  - iOS app runtime loads `activeGoal` with conversation state
  - iOS workspace screens render a goal summary/create card with create/edit/
    pause/resume/clear actions when `capabilities.goals=true`
  - Android conversation binding loads `goal`
  - Android phone/tablet workspace panes accept and render goal summary state
    with create/edit/pause/resume/clear actions when `capabilities.goals=true`
  - design pass improved parity with web:
    - semantic status chips
    - visible token/time usage
    - budget progress bars
    - humanized time labels
    - confirm-before-clear on iOS and Android
- workspace feature gating for goals:
  - default bootstrap config seeds `features.goals.enabled: true`
  - workspace metadata advertises `capabilities.goals=true` only when that
    feature stays enabled, goal tools are actually exposed, and `system/goal`
    remains enabled in internal MCP services
  - backend enforcement now matches the UI gate:
    - embedded conversation goal API rejects goal reads/writes when disabled
    - `system/goal` tool methods reject calls when disabled
    - `agently` runtime skips registering `system/goal` when disabled
- SQLite compatibility migration for legacy `goal` tables missing
  `status_reason`
- MySQL versioned migration entries for `goal` / `status_reason`
- user-facing `/goal` composer command (web) as a thin adapter over the
  conversation goal API (`parseGoalCommand` / `handleGoalCommand` in
  `agently/ui/src/services/chatService.js`), supporting show/set/pause/resume/
  clear with the conversation id sourced from context
- truthful controller snapshot inputs in `service/agent/goal_runtime.go`:
  - `PendingApproval` from `tool_approval_queue` pending count
  - `PendingElicitation` from unresolved elicitation message count
  - `PendingAsync` from the in-memory async manager's non-terminal
    conversation-scoped operation list
  - durable controller state on the goal row:
    - `autonomous_turns_used`
    - `consecutive_no_progress`
    - `last_continuation_fingerprint`
  - `AutonomousTurnsUsed` and `ConsecutiveNoProgress` now persist across
    turns/restarts instead of being purely inferred
  - new Datly count components mirroring `turn/queuedCount`:
    `turn/controllerCount`, `toolapprovalqueue/pendingCount`,
    `message/elicitationCount`
  - gathered via a narrow capability interface with safe zero-value
    degradation for durable/read-side signals
- richer continuation-hint derivation in `service/agent`:
  - prefers explicit continuation hints embedded in the last turn content
  - otherwise derives the hint from the first meaningful `QueryOutput.Plan`
    step
  - only falls back to the generic goal/objective continuation when no better
    signal exists
- richer progress fingerprinting for no-progress detection:
  - fingerprints now incorporate assistant content + summarized plan steps +
    continuation fields
  - `ConsecutiveNoProgress` no longer depends only on continuation text
    similarity
- async-completion policy is now active in the current turn rerun path:
  - `OnAsyncCompleted=evaluate` preserves async rerun-after-change behavior
  - `OnAsyncCompleted=wait` suppresses the automatic rerun after async
    completion for the current turn
- detached async completion now has an autonomous bridge:
  - non-wait async ops register a one-shot completion observer
  - when such an op later completes successfully and the conversation is idle,
    the runtime re-enters the goal controller and may enqueue a controller
    continuation turn
  - overlapping detached completions for the same conversation are serialized
    so they do not burst multiple duplicate controller continuations
  - the shared controller enqueue path now also skips queueing when the same
    goal already has a queued controller-owned turn in the conversation

## Verified

- `go test ./pkg/agently/goal/... ./app/store/data -run 'Test(DataService_(GoalPredicates|Patch_DataDriven)|GoalRead_SQLite)$'`
- `go test ./protocol/tool/service/system/goal/... ./internal/service/sqlite/...`
- `go test ./service/goal/... ./service/agent/...`
- `go test ./runtime/...`
- `go test ./sdk/... ./runtime/...`
- `go test ./bootstrap/...`
- `APPSERVER_URL=http://127.0.0.1:9191 npx vitest run src/components/ToolFeedDetail.test.js src/services/chatRuntime.test.js src/services/toolFeedBus.test.js` in `agently/ui`
  (default goal feed spec path + local goal data refresh + scoped feed fetch behavior)
- `go test ./workspace/config/... ./service/workspace -run 'Test(GoalsEnabled|MetadataHandler_(SetsGoalsCapabilityWhenGoalToolsAreExposed|ClearsGoalsCapabilityWhenWorkspaceDisablesGoals|StarterTasks|DescriptorInfos|IncludesInternalFlagForAgents|SortsAgentAndModelInfosByLabel))'`
- `go test ./sdk/... ./protocol/tool/service/system/goal/...` (goal feature-disabled server-side enforcement)
- `go test ./runtime/...` in `agently` (skip `system/goal` registration when the workspace disables goals)
- `go test ./protocol/tool/service/system/goal/... ./service/agent/... ./service/goal/... ./service/shared/toolexec/...`
  (goal lifecycle stream events + controller scheduling event surface)
- `APPSERVER_URL=http://127.0.0.1:9191 npx vitest run src/services/chatRuntime.test.js src/components/GoalSummaryStrip.test.jsx src/services/chatService.goal.test.js src/components/ToolFeedDetail.test.js` in `agently/ui`
  (web shell consumes goal lifecycle events into the live goal feed)
- `npx vitest run src/__tests__/conversationStream.test.ts` in `sdk/ts`
  (TS SDK stream tracker consumes goal lifecycle events into feeds)
- `swift test` in `sdk/ios`
  (iOS SDK stream tracker compiles/tests with goal lifecycle feed handling)
- `./gradlew test` in `sdk/android`
  (Android SDK stream tracker compiles/tests with goal lifecycle feed handling)
- `npx vitest run src/__tests__/client.test.ts` in `sdk/ts`
- `swift test` in `sdk/ios`
- `./gradlew test` in `sdk/android`
- `APPSERVER_URL=http://127.0.0.1:9191 npx vitest run src/services/chatService.init.test.js src/services/chatRuntime.test.js src/components/ToolFeedWorkspace.test.jsx` in `agently/ui`
- `APPSERVER_URL=http://127.0.0.1:9191 npx vitest run src/components/chat/SteerQueue.test.jsx src/services/renderRows.test.js src/services/chatRuntime.test.js src/components/ToolFeedWorkspace.test.jsx` in `agently/ui`
- `./gradlew :agently-core-sdk:compileDebugKotlin :agently-core-sdk:testDebugUnitTest` in `agently/android`
- `./gradlew :app:compileDebugKotlin :app:compileReleaseKotlin` in `agently/android`
- `xcodebuild -project /Users/awitas/go/src/github.com/viant/agently/ios/AgentlyApp.xcodeproj -scheme AgentlyApp -destination 'generic/platform=iOS Simulator' build`
- `./gradlew :app:compileDebugKotlin :app:compileReleaseKotlin` in `agently/android`
  after the mobile goal-card design/confirmation pass
- `xcodebuild -project /Users/awitas/go/src/github.com/viant/agently/ios/AgentlyApp.xcodeproj -scheme AgentlyApp -destination 'generic/platform=iOS Simulator' build`
  after the mobile goal-card design/confirmation pass
- `go test ./internal/service/sqlite/... ./service/agent/... ./sdk/...`
- `APPSERVER_URL=http://127.0.0.1:9191 npx vitest run src/services/chatRuntime.test.js src/services/renderRows.test.js` in `agently/ui`
- `go test ./app/store/data/ -run TestDataService_ControllerSnapshotCounts` (new
  count components + data-service methods against SQLite)
- `go test ./service/agent/ -run TestGatherControllerSignals` (snapshot signal
  gathering with truthful/zero/error/incapable cases)
- `go test ./service/goal/... ./service/agent/... ./internal/service/sqlite/...` after adding durable
  goal controller-state columns and runtime/state projection
- `go test ./service/agent/... ./service/goal/...`
  (richer continuation-hint derivation from explicit content + plan steps)
- `go test ./service/agent/... ./service/goal/...`
  (async-completion policy now affects rerun-after-change behavior)
- `go test ./service/agent/... ./service/goal/...`
  (progress fingerprint now tracks actual outcome changes, not only continuation similarity)
- `go test ./service/agent/... ./service/goal/... ./service/shared/toolexec/...`
  (detached async completion bridge into autonomous goal continuation)
- `go test ./service/agent/... ./service/goal/... ./service/shared/toolexec/...`
  (duplicate continuation suppression across concurrent detached async completions)
- `go test ./service/agent/... ./service/goal/... ./service/shared/toolexec/...`
  (skip second controller queue entry when the goal already has one queued continuation)
- `APPSERVER_URL=http://127.0.0.1:9191 npx vitest run src/services/chatService.goal.test.js` in `agently/ui`
  (parse/handle `/goal` and submit routing)
- `APPSERVER_URL=http://127.0.0.1:9191 npx vitest run src/components/GoalSummaryStrip.test.jsx src/components/ToolFeedDetail.test.js src/services/chatService.goal.test.js` in `agently/ui`
  (design-pass goal strip/panel UX updates)
- `APPSERVER_URL=http://127.0.0.1:9191 npx vitest run src/components/GoalDraftDialog.test.jsx src/components/GoalSummaryStrip.test.jsx src/services/chatService.goal.test.js src/components/ToolFeedDetail.test.js src/components/Root.test.jsx` in `agently/ui`
  (guided autonomous goal drafting thin + strip entrypoint wiring)
- `APPSERVER_URL=http://127.0.0.1:9191 npx vitest run src/components/ToolFeedDetail.test.js src/services/chatService.goal.test.js src/components/ToolFeedWorkspace.test.jsx src/components/chat/SteerQueue.test.jsx` in `agently/ui`
  (create/edit goal feed actions + `/goal` routing)
- `APPSERVER_URL=http://127.0.0.1:9191 npx vitest run src/components/GoalSummaryStrip.test.jsx src/components/ToolFeedDetail.test.js src/services/chatService.goal.test.js` in `agently/ui`
  (dedicated summary strip + feed editing + `/goal` routing)
- `APPSERVER_URL=http://127.0.0.1:9191 npx vitest run src/services/chatService.goal.test.js src/components/ToolFeedDetail.test.js src/services/feedForgeContext.test.js` in `agently/ui`
  (feed-native goal handlers + Forge-rendered goal feed opt-in)
- `APPSERVER_URL=http://127.0.0.1:9191 npx vitest run src/services/chatService.submit.test.js src/services/chatService.test.js` in `agently/ui` (no regression from the `/goal` interceptor)

## Default-workspace verification notes

- bootstrap defaults now expose `system/goal` end to end:
  - internal MCP service enabled in default `config.yaml`
  - `coder` and `chatter` include the `system/goal` bundle
- runtime integration test executes an explicit goal task against the real
  `system/goal` service path:
  - create goal with objective `finish parser cleanup`
  - fetch current goal
  - update goal to `blocked` with durable reason
- web SDK verification:
  - TypeScript client test confirms conversation-scoped direct execution of
    `system/goal:create`
- public goal route verification:
  - Go SDK/runtime tests compile and exercise the backend goal surface
  - TypeScript client tests cover `getGoal`, `createGoal`, and `updateGoal`
  - iOS SDK tests cover `getGoal`, `createGoal`, `updateGoal`, and `clearGoal`
- visible web verification:
  - dedicated goal summary strip renders for both active-goal and no-goal states
    and is hidden when the workspace goal feature is disabled
  - chat runtime tests pass with goal-feed refresh wiring enabled
  - tool-feed workspace tests pass with configured Vite backend env
  - goal feed renders visible create/edit/pause/resume/clear actions
  - goal feed can also render through the shared Forge feed container path when
    the feed payload opts into `renderMode: forge`
  - default goal feed schema is available through workspace `feeds/goal.yaml`
    instead of requiring the entire panel definition to stay inline in
    `chatRuntime`
  - status, budget, and quick-action hierarchy now render in the strip/panel
    instead of only raw text/buttons
  - no-goal web state now has a dedicated structured drafting dialog rather
    than only a bare single-line create field
  - `/goal` command routing is blocked with a workspace warning when
    `capabilities.goals=false`
  - steer queue tests cover controller-owned queued turns showing the `AUTO`
    badge and durable continuation reason
- controller queue metadata verification:
  - SQLite migration test covers legacy `turn` upgrades for `origin`, `goal_id`,
    and `status_reason`
  - Go agent/sdk suites pass with controller-owned queued turns carrying new
    metadata fields
  - web render/runtime tests pass with queued-turn preview extensions
  - canonical render-row tests preserve `origin`, `goalId`, and `statusReason`
    on queued turn previews
  - goal service tests cover `goal.updated` / `goal.cleared`
  - agent tests cover direct `goal.controller_scheduled` publication
  - web and SDK trackers consume goal lifecycle events as live feed updates
  - iOS and Android app runtimes now apply streamed `goal` feed state to
    mobile `activeGoal`
- durable controller-state verification:
  - SQLite compatibility migration covers legacy `goal` upgrades for
    `autonomous_turns_used`, `consecutive_no_progress`, and
    `last_continuation_fingerprint`
  - goal runtime tests cover projected autonomous-turn and repeated-fingerprint
    no-progress state
  - goal store tests cover persistence writes for controller-state fields
  - agent tests cover continuation-hint precedence:
    explicit content continuation > plan-derived continuation > generic goal fallback
  - agent tests cover async-completion rerun policy:
    `AsyncPolicyWait` suppresses rerun after completion while
    `AsyncPolicyEvaluate` keeps it
  - agent tests cover progress-fingerprint change detection across content/plan
  - agent/toolexec tests cover detached async completion bridging:
    `AsyncPolicyEvaluate` may queue a controller continuation after async
    success, while `AsyncPolicyWait` suppresses it
  - agent tests also cover duplicate suppression when multiple detached async
    ops complete near-simultaneously for the same conversation
  - agent tests cover the stronger invariant that one goal should not accumulate
    multiple queued controller continuations at once
- mobile SDK verification:
  - iOS SDK tests confirm conversation-scoped `system/goal:create`
  - Android SDK unit tests/build pass after the new optional `conversationId`
    parameter on direct tool execution and the new goal route methods
- mobile app verification:
  - Android app module compile passes with the new goal-summary action path
    (`Create`, `Edit`, `Pause`, `Resume`, `Clear`) and capability gating
  - iOS app Xcode project now builds successfully for the simulator with the
    goal-summary action path (`Create`, `Edit`, `Pause`, `Resume`, `Clear`) and
    capability gating in place
  - both mobile apps now compile with status-chip, budget-bar, humanized-time,
    and confirm-before-clear goal-card polish
- server-side goal disablement verification:
  - workspace config tests cover `features.goals.enabled`
  - workspace metadata tests cover capability suppression when disabled
  - goal tool tests cover disabled `system/goal` execution
  - embedded goal tests cover disabled conversation goal API writes
  - runtime tests cover skipped `system/goal` registration when disabled

## Remaining

- richer runtime/controller continuation hooks beyond the current default
  continuation payload
- richer native/web goal presentation and interaction on top of the current
  summary/feed surfaces (web now has a dedicated summary strip, `/goal`, and
  minimal feed actions; mobile now has basic create/edit/pause/resume/clear
  actions but no richer goal editor)
- richer semantic progress detection beyond the current repeated-continuation
  fingerprint heuristic
