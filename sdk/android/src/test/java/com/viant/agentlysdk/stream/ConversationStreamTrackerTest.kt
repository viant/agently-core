package com.viant.agentlysdk.stream

import com.viant.agentlysdk.AssistantMessageState
import com.viant.agentlysdk.AssistantState
import com.viant.agentlysdk.ConversationState
import com.viant.agentlysdk.ConversationStateResponse
import com.viant.agentlysdk.ExecutionPageState
import com.viant.agentlysdk.ExecutionState
import com.viant.agentlysdk.ModelStepState
import com.viant.agentlysdk.PlannerState
import com.viant.agentlysdk.RenderedContent
import com.viant.agentlysdk.RenderedReportAssembly
import com.viant.agentlysdk.ToolStepState
import com.viant.agentlysdk.TurnMessageState
import com.viant.agentlysdk.TurnState
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import java.io.File
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

class ConversationStreamTrackerTest {
    @Test
    fun `tool feed target and owning turn survive live and canonical tracking`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.applyEvent(
            SSEEvent(
                type = "tool_feed_active",
                conversationId = "conv-1",
                turnId = "turn-live",
                feedId = "plan",
                feedTitle = "Plan",
                feedTarget = "inline",
                feedItemCount = 2
            )
        )

        assertEquals("inline", tracker.snapshot().feeds.single().presentation?.target)
        assertEquals("turn-live", tracker.snapshot().feeds.single().turnId)

        val hydratedTracker = ConversationStreamTracker("conv-1")
        hydratedTracker.hydrate(
            ConversationStateResponse(
                feeds = listOf(
                    com.viant.agentlysdk.ActiveFeedState(
                        feedId = "plan",
                        title = "Plan",
                        itemCount = 2,
                        turnId = "turn-canonical",
                        presentation = com.viant.agentlysdk.FeedPresentation(target = "detached")
                    )
                )
            )
        )

        assertEquals("detached", hydratedTracker.snapshot().feeds.single().presentation?.target)
        assertEquals("turn-canonical", hydratedTracker.snapshot().feeds.single().turnId)
    }

    @Test
    fun `hydrate projects only primary execution page into assistant messages`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.hydrate(
            ConversationStateResponse(
                conversation = ConversationState(
                    conversationId = "conv-1",
                    turns = listOf(
                        TurnState(
                            turnId = "turn-1",
                            status = "running",
                            execution = ExecutionState(
                                pages = listOf(
                                    ExecutionPageState(
                                        pageId = "narrator-page",
                                        assistantMessageId = "narrator-message",
                                        executionRole = "narrator",
                                        content = "internal narration payload"
                                    ),
                                    ExecutionPageState(
                                        pageId = "worker-page",
                                        assistantMessageId = "worker-message",
                                        executionRole = "worker",
                                        content = "internal worker payload"
                                    ),
                                    ExecutionPageState(
                                        pageId = "intermediate-react-page",
                                        assistantMessageId = "intermediate-react-message",
                                        executionRole = "react",
                                        finalResponse = false,
                                        content = "internal intermediate model payload"
                                    ),
                                    ExecutionPageState(
                                        pageId = "primary-page",
                                        assistantMessageId = "primary-message",
                                        executionRole = "react",
                                        finalResponse = true,
                                        content = "Visible streaming response"
                                    )
                                )
                            )
                        )
                    )
                )
            )
        )

        val messages = tracker.snapshot().bufferedMessages
        assertEquals(listOf("primary-message"), messages.map { it.id })
        assertEquals("Visible streaming response", messages.single().content)
    }


    @Test
    fun `shared progressive rendered content fixture decodes`() {
        val fixture = File("../testdata/rendered_content_progressive.json").readText()
        val decoded = Json { ignoreUnknownKeys = true }.decodeFromString<RenderedContent>(fixture)

        val report = decoded.reports.single()
        assertEquals("campaign", report.scope)
        assertEquals("delivery", report.id)
        assertEquals("dashboard-v1", report.grammar)
        assertEquals("committed", report.status)
        assertEquals(3, report.sequence)
        assertEquals(1, report.resetVersion)
        assertEquals("Delivery", report.source!!.jsonObject.getValue("title").jsonPrimitive.content)
        assertEquals(1, report.source!!.jsonObject.getValue("blocks").jsonArray.size)
        assertEquals("delivery", report.dataSources["rows"]?.reportRef)
        assertEquals("CTV", report.dataSources.getValue("rows").payload!!.jsonArray.first().jsonObject.getValue("channel").jsonPrimitive.content)
    }

    @Test
    fun `stream events carry canonical progressive reports into the live execution group`() {
        val tracker = ConversationStreamTracker("conv-1")
        val rendered = RenderedContent(
            schemaVersion = "1",
            reports = listOf(
                RenderedReportAssembly(
                    scope = "campaign",
                    id = "delivery",
                    status = "committed"
                )
            )
        )

        tracker.applyEvent(
            SSEEvent(
                type = "text_delta",
                conversationId = "conv-1",
                turnId = "turn-1",
                assistantMessageId = "assistant-1",
                content = "report fragment",
                renderedContent = rendered
            )
        )

        val group = tracker.snapshot().liveExecutionGroupsById["assistant-1"]
        assertNotNull(group)
        assertEquals("delivery", group.renderedContent?.reports?.single()?.id)
    }

    @Test
    fun `transcript hydration restores canonical progressive reports`() {
        val tracker = ConversationStreamTracker("conv-1")
        val rendered = RenderedContent(
            schemaVersion = "1",
            reports = listOf(
                RenderedReportAssembly(
                    scope = "campaign",
                    id = "delivery",
                    status = "committed"
                )
            )
        )

        tracker.hydrate(
            ConversationStateResponse(
                conversation = ConversationState(
                    conversationId = "conv-1",
                    turns = listOf(
                        TurnState(
                            turnId = "turn-1",
                            status = "completed",
                            execution = ExecutionState(
                                pages = listOf(
                                    ExecutionPageState(
                                        pageId = "page-1",
                                        assistantMessageId = "assistant-1",
                                        renderedContent = rendered
                                    )
                                )
                            )
                        )
                    )
                )
            )
        )

        assertEquals(
            "delivery",
            tracker.snapshot().liveExecutionGroupsById["assistant-1"]?.renderedContent?.reports?.single()?.id
        )
    }

    @Test
    fun `late duplicated message patch does not overwrite clean final assistant content`() {
        val tracker = ConversationStreamTracker("conv-1")
        val messageId = "msg-1"
        val turnId = "turn-1"
        val cleanFinal = """
            1. Got it — here’s a short, structured response.

            2. Kotlin example:
            ```kotlin
            fun reply(): String = "Hello!"
            ```
        """.trimIndent()
        val duplicatedPatch = """
            1. Got it — here’s a short, structured response.

            2. Kotlin example:
            ```kotlinfun reply(): String = "Hello!"
            ```1. Got it — here’s a short, structured response.

            2. Kotlin example:
            ```kotlin
            fun reply(): String = "Hello!"
            ```
        """.trimIndent()

        tracker.applyEvent(
            SSEEvent(
                id = messageId,
                conversationId = "conv-1",
                turnId = turnId,
                messageId = messageId,
                assistantMessageId = messageId,
                type = "assistant",
                content = cleanFinal,
                patch = buildJsonObject {
                    put("role", "assistant")
                }
            )
        )
        tracker.applyEvent(
            SSEEvent(
                id = messageId,
                conversationId = "conv-1",
                turnId = turnId,
                messageId = messageId,
                type = "control",
                op = "message_patch",
                patch = buildJsonObject {
                    put("content", duplicatedPatch)
                    put("interim", 0)
                }
            )
        )

        val snapshot = tracker.snapshot()
        val assistant = snapshot.bufferedMessages.single { it.id == messageId }
        assertEquals(cleanFinal, assistant.content)
        assertEquals(0, assistant.interim)
    }

    @Test
    fun `collapseRepeatedContent keeps last clean segment`() {
        val input = """
            Hello there.
            ```kotlinfun hi() = "x"
            ```Hello there.
            ```kotlin
            fun hi() = "x"
            ```
        """.trimIndent()

        val collapsed = collapseRepeatedContent(input)

        assertEquals(
            """
            Hello there.
            ```kotlin
            fun hi() = "x"
            ```
            """.trimIndent(),
            collapsed
        )
    }

    @Test
    fun `planner SSE events update turn planner state`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.applyEvent(
            SSEEvent(
                type = "turn_started",
                conversationId = "conv-1",
                turnId = "turn-1"
            )
        )
        tracker.applyEvent(
            SSEEvent(
                type = "planner.selected",
                conversationId = "conv-1",
                turnId = "turn-1",
                plannerTrigger = "exploratory_strategy",
                plannerStaticProfile = "repo_analysis"
            )
        )
        tracker.applyEvent(
            SSEEvent(
                type = "planner.output",
                conversationId = "conv-1",
                turnId = "turn-1",
                plannerStrategyFamily = "troubleshoot",
                plannerAttempt = 1,
                plannerOutputPayloadId = "planner-output:conv-1:turn-1"
            )
        )
        tracker.applyEvent(
            SSEEvent(
                type = "planner.validated",
                conversationId = "conv-1",
                turnId = "turn-1",
                plannerAttempt = 1,
                plannerValidated = true
            )
        )

        val planner = tracker.snapshot().plannerByTurnId["turn-1"]
        assertNotNull(planner)
        assertEquals("validated", planner.status)
        assertEquals("exploratory_strategy", planner.trigger)
        assertEquals("repo_analysis", planner.staticProfile)
        assertEquals("troubleshoot", planner.strategyFamily)
        assertEquals(1, planner.attempt)
        assertEquals("planner-output:conv-1:turn-1", planner.outputPayloadId)
        assertEquals(true, planner.validated)
    }

    @Test
    fun `tool completion preserves response payload in live execution group`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.applyEvent(
            SSEEvent(
                type = "tool_call_completed",
                conversationId = "conv-1",
                turnId = "turn-1",
                assistantMessageId = "assistant-1",
                toolCallId = "tool-1",
                toolName = "ui/view/open",
                responsePayload = buildJsonObject {
                    put("windowId", "reportWindow__conv-1")
                    put("conversationId", "conv-1")
                    put("windowKey", "reportWindow")
                    put("presentation", "hosted")
                }
            )
        )

        val step = tracker.snapshot()
            .liveExecutionGroupsById["assistant-1"]
            ?.toolSteps
            ?.singleOrNull()

        assertNotNull(step)
        assertEquals("completed", step.status)
        assertEquals(
            "reportWindow__conv-1",
            step.responsePayload?.jsonObject?.get("windowId")?.jsonPrimitive?.content
        )
    }

    @Test
    fun `elicitation request preserves status in pending state`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.applyEvent(
            SSEEvent(
                type = "elicitation_requested",
                conversationId = "conv-1",
                turnId = "turn-1",
                elicitationId = "elicit-1",
                content = "Approve this?",
                status = "resolved",
                elicitationData = buildJsonObject {
                    put(
                        "requestedSchema",
                        buildJsonObject {
                            put("type", "object")
                        }
                    )
                }
            )
        )

        val pending = tracker.snapshot().pendingElicitation
        assertNotNull(pending)
        assertEquals("elicit-1", pending.elicitationId)
        assertEquals("conv-1", pending.conversationId)
        assertEquals("turn-1", pending.turnId)
        assertEquals("Approve this?", pending.message)
        assertEquals("resolved", pending.status)
    }

    @Test
    fun `tool completion preserves non object request and response payloads`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.applyEvent(
            SSEEvent(
                type = "tool_call_completed",
                conversationId = "conv-1",
                turnId = "turn-1",
                assistantMessageId = "assistant-1",
                toolCallId = "tool-1",
                toolName = "ui/window/show",
                arguments = JsonPrimitive("window-1"),
                responsePayload = buildJsonArray {
                    add(JsonPrimitive("ok"))
                    add(JsonPrimitive(true))
                }
            )
        )

        val step = tracker.snapshot()
            .liveExecutionGroupsById["assistant-1"]
            ?.toolSteps
            ?.singleOrNull()

        assertNotNull(step)
        assertEquals(JsonPrimitive("window-1"), step.requestPayload)
        assertEquals("""["ok",true]""", step.responsePayload.toString())
    }

    @Test
    fun `tool completion merges turn metadata into existing live execution group`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.applyEvent(
            SSEEvent(
                type = "model_started",
                conversationId = "conv-1",
                assistantMessageId = "assistant-1",
                model = EventModel(provider = "openai", model = "gpt-5-mini")
            )
        )
        tracker.applyEvent(
            SSEEvent(
                type = "tool_call_completed",
                conversationId = "conv-1",
                turnId = "turn-1",
                eventSeq = 7,
                iteration = 3,
                assistantMessageId = "assistant-1",
                toolCallId = "tool-1",
                toolName = "ui/view/open",
                responsePayload = buildJsonObject {
                    put("windowId", "window-1")
                    put("windowKey", "report")
                }
            )
        )

        val group = tracker.snapshot().liveExecutionGroupsById["assistant-1"]
        assertEquals("turn-1", group?.turnId)
        assertEquals(7, group?.sequence)
        assertEquals(3, group?.iteration)
    }

    @Test
    fun `tool completion preserves tool order when merging updates`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.applyEvent(
            SSEEvent(
                type = "tool_call_started",
                conversationId = "conv-1",
                turnId = "turn-1",
                assistantMessageId = "assistant-1",
                toolCallId = "tool-1",
                toolName = "first-tool"
            )
        )
        tracker.applyEvent(
            SSEEvent(
                type = "tool_call_started",
                conversationId = "conv-1",
                turnId = "turn-1",
                assistantMessageId = "assistant-1",
                toolCallId = "tool-2",
                toolName = "second-tool"
            )
        )
        tracker.applyEvent(
            SSEEvent(
                type = "tool_call_completed",
                conversationId = "conv-1",
                turnId = "turn-1",
                assistantMessageId = "assistant-1",
                toolCallId = "tool-1",
                toolName = "first-tool",
                responsePayload = buildJsonObject { put("ok", true) }
            )
        )

        val steps = tracker.snapshot().liveExecutionGroupsById["assistant-1"]?.toolSteps.orEmpty()
        assertEquals(listOf("tool-1", "tool-2"), steps.map { it.toolCallId.orEmpty() })
        assertEquals(listOf("completed", "running"), steps.map { it.status.orEmpty() })
    }

    @Test
    fun `hydrate restores transcript execution groups and active turn`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.hydrate(
            ConversationStateResponse(
                eventCursor = "2026-06-05T10:00:00Z",
                conversation = ConversationState(
                    conversationId = "conv-1",
                    turns = listOf(
                        TurnState(
                            turnId = "turn-live",
                            status = "running",
                            createdAt = "2026-06-05T09:59:59Z",
                            assistant = AssistantState(
                                final = AssistantMessageState(
                                    messageId = "assistant-1",
                                    content = "Opening the workspace"
                                )
                            ),
                            execution = ExecutionState(
                                pages = listOf(
                                    ExecutionPageState(
                                        pageId = "page-1",
                                        assistantMessageId = "assistant-1",
                                        turnId = "turn-live",
                                        parentMessageId = "user-1",
                                        sequence = 7,
                                        iteration = 2,
                                        status = "running",
                                        narration = "Working",
                                        modelSteps = listOf(
                                            ModelStepState(
                                                modelCallId = "model-1",
                                                assistantMessageId = "assistant-1",
                                                provider = "openai",
                                                model = "gpt-5-mini",
                                                status = "completed"
                                            )
                                        ),
                                        toolSteps = listOf(
                                            ToolStepState(
                                                toolCallId = "tool-1",
                                                toolMessageId = "tool-message-1",
                                                toolName = "ui/view/open",
                                                status = "completed",
                                                responsePayload = buildJsonObject {
                                                    put("windowId", "window-1")
                                                    put("windowKey", "report")
                                                }
                                            )
                                        )
                                    )
                                )
                            )
                        )
                    )
                )
            )
        )

        val snapshot = tracker.snapshot()
        assertEquals("turn-live", snapshot.activeTurnId)
        val group = snapshot.liveExecutionGroupsById["assistant-1"]
        assertNotNull(group)
        assertEquals("turn-live", group.turnId)
        assertEquals(7, group.sequence)
        assertEquals(2, group.iteration)
        assertEquals("Working", group.narration)
        assertEquals("gpt-5-mini", group.modelSteps.single().model)
        assertEquals("window-1", group.toolSteps.single().responsePayload?.jsonObject?.get("windowId")?.jsonPrimitive?.content)
    }

    @Test
    fun `hydrate uses cursor not transcript message sequence when applying live delta`() {
        val tracker = ConversationStreamTracker("conv-1")
        val cursor = "2026-06-05T10:00:00Z"

        tracker.hydrate(
            ConversationStateResponse(
                eventCursor = cursor,
                conversation = ConversationState(
                    conversationId = "conv-1",
                    turns = listOf(
                        TurnState(
                            turnId = "turn-1",
                            status = "running",
                            messages = listOf(
                                TurnMessageState(
                                    messageId = "assistant-1",
                                    role = "assistant",
                                    content = "Hello",
                                    sequence = 100
                                )
                            ),
                            assistant = AssistantState(
                                final = AssistantMessageState(
                                    messageId = "assistant-1",
                                    content = "Hello"
                                )
                            )
                        )
                    )
                )
            )
        )

        tracker.applyEvent(
            SSEEvent(
                type = "text_delta",
                conversationId = "conv-1",
                turnId = "turn-1",
                messageId = "assistant-1",
                assistantMessageId = "assistant-1",
                eventSeq = 7,
                content = " duplicate",
                createdAt = cursor
            ),
            hydrationCursor = cursor
        )
        assertEquals("Hello", tracker.snapshot().bufferedMessages.single { it.id == "assistant-1" }.content)
        tracker.applyEvent(
            SSEEvent(
                type = "text_delta",
                conversationId = "conv-1",
                turnId = "turn-1",
                messageId = "assistant-1",
                assistantMessageId = "assistant-1",
                content = " stale",
                createdAt = cursor
            ),
            hydrationCursor = cursor
        )
        assertEquals("Hello", tracker.snapshot().bufferedMessages.single { it.id == "assistant-1" }.content)
        tracker.applyEvent(
            SSEEvent(
                type = "text_delta",
                conversationId = "conv-1",
                turnId = "turn-1",
                messageId = "assistant-1",
                assistantMessageId = "assistant-1",
                eventSeq = 100,
                content = " duplicate",
                createdAt = "2026-06-05T10:00:01Z"
            ),
            hydrationCursor = cursor
        )
        assertEquals("Hello", tracker.snapshot().bufferedMessages.single { it.id == "assistant-1" }.content)
        tracker.applyEvent(
            SSEEvent(
                type = "text_delta",
                conversationId = "conv-1",
                turnId = "turn-1",
                messageId = "assistant-1",
                assistantMessageId = "assistant-1",
                eventSeq = 1,
                content = " live",
                createdAt = "2026-06-05T10:00:01Z"
            ),
            hydrationCursor = cursor
        )

        val assistant = tracker.snapshot().bufferedMessages.single { it.id == "assistant-1" }
        assertEquals("Hello live", assistant.content)
    }

    @Test
    fun `hydrate replays a late join delta after the authoritative transcript prefix`() {
        val tracker = ConversationStreamTracker("conv-1")
        tracker.applyEvent(
            SSEEvent(
                type = "text_delta",
                conversationId = "conv-1",
                turnId = "turn-1",
                messageId = "assistant-1",
                assistantMessageId = "assistant-1",
                eventSeq = 8,
                content = "inside fence\n```",
                contentMode = "delta",
                contentOffset = "Visible summary\n```forge-data\n".toByteArray(Charsets.UTF_8).size,
                createdAt = "2026-06-05T10:00:01Z"
            )
        )

        tracker.hydrate(
            ConversationStateResponse(
                eventCursor = "2026-06-05T10:00:00Z",
                conversation = ConversationState(
                    conversationId = "conv-1",
                    turns = listOf(
                        TurnState(
                            turnId = "turn-1",
                            status = "running",
                            assistant = AssistantState(
                                final = AssistantMessageState(
                                    messageId = "assistant-1",
                                    content = "Visible summary\n```forge-data\n"
                                )
                            )
                        )
                    )
                )
            )
        )

        assertEquals(
            "Visible summary\n```forge-data\ninside fence\n```",
            tracker.snapshot().bufferedMessages.single { it.id == "assistant-1" }.content
        )
    }

    @Test
    fun `hydrate falls back to transcript message sequence when cursor is unavailable`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.hydrate(
            ConversationStateResponse(
                conversation = ConversationState(
                    conversationId = "conv-1",
                    turns = listOf(
                        TurnState(
                            turnId = "turn-1",
                            status = "running",
                            messages = listOf(
                                TurnMessageState(
                                    messageId = "assistant-1",
                                    role = "assistant",
                                    content = "Hello",
                                    sequence = 7
                                )
                            ),
                            assistant = AssistantState(
                                final = AssistantMessageState(
                                    messageId = "assistant-1",
                                    content = "Hello"
                                )
                            )
                        )
                    )
                )
            )
        )

        tracker.applyEvent(
            SSEEvent(
                type = "text_delta",
                conversationId = "conv-1",
                turnId = "turn-1",
                messageId = "assistant-1",
                assistantMessageId = "assistant-1",
                eventSeq = 7,
                content = " duplicate"
            )
        )
        assertEquals("Hello", tracker.snapshot().bufferedMessages.single { it.id == "assistant-1" }.content)

        tracker.applyEvent(
            SSEEvent(
                type = "text_delta",
                conversationId = "conv-1",
                turnId = "turn-1",
                messageId = "assistant-1",
                assistantMessageId = "assistant-1",
                eventSeq = 8,
                content = " live"
            )
        )

        assertEquals("Hello live", tracker.snapshot().bufferedMessages.single { it.id == "assistant-1" }.content)
    }

    @Test
    fun `transcript hydrate does not overwrite active turn message owned by SSE`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.applyEvent(
            SSEEvent(
                type = "turn_started",
                conversationId = "conv-1",
                turnId = "turn-live"
            )
        )
        tracker.applyEvent(
            SSEEvent(
                type = "assistant",
                conversationId = "conv-1",
                turnId = "turn-live",
                messageId = "assistant-live",
                assistantMessageId = "assistant-live",
                content = "SSE active response",
                patch = buildJsonObject {
                    put("role", "assistant")
                }
            )
        )
        tracker.applyEvent(
            SSEEvent(
                type = "model_started",
                conversationId = "conv-1",
                turnId = "turn-live",
                assistantMessageId = "assistant-live",
                status = "running"
            )
        )

        tracker.hydrate(
            ConversationStateResponse(
                conversation = ConversationState(
                    conversationId = "conv-1",
                    turns = listOf(
                        TurnState(
                            turnId = "turn-history",
                            status = "completed",
                            assistant = AssistantState(
                                final = AssistantMessageState(
                                    messageId = "assistant-history",
                                    content = "Historical response"
                                )
                            )
                        ),
                        TurnState(
                            turnId = "turn-live",
                            status = "running",
                            execution = ExecutionState(
                                pages = listOf(
                                    ExecutionPageState(
                                        pageId = "stale-page",
                                        assistantMessageId = "assistant-live",
                                        turnId = "turn-live",
                                        status = "completed",
                                        content = "stale transcript execution"
                                    )
                                )
                            ),
                            assistant = AssistantState(
                                final = AssistantMessageState(
                                    messageId = "assistant-live",
                                    content = "stale transcript response"
                                )
                            )
                        )
                    )
                )
            )
        )

        val messages = tracker.snapshot().bufferedMessages
        assertEquals("turn-live", tracker.snapshot().activeTurnId)
        assertEquals("SSE active response", messages.single { it.id == "assistant-live" }.content)
        assertEquals("Historical response", messages.single { it.id == "assistant-history" }.content)
        assertEquals("running", tracker.snapshot().liveExecutionGroupsById["assistant-live"]?.status)
        assertEquals(null, tracker.snapshot().liveExecutionGroupsById["assistant-live"]?.content)
    }

    @Test
    fun `transcript hydrate populates planner state for past turns`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.reconcileTranscript(
            listOf(
                TurnState(
                    turnId = "turn-past",
                    status = "completed",
                    planner = PlannerState(
                        status = "failed",
                        trigger = "low_confidence",
                        strategyFamily = "troubleshoot",
                        attempt = 2,
                        secondPolicy = "clarify",
                        outputPayloadId = "planner-output:conv-1:turn-past",
                        validated = false
                    )
                )
            )
        )

        val planner = tracker.snapshot().plannerByTurnId["turn-past"]
        assertNotNull(planner)
        assertEquals("failed", planner.status)
        assertEquals("low_confidence", planner.trigger)
        assertEquals("troubleshoot", planner.strategyFamily)
        assertEquals(2, planner.attempt)
        assertEquals("clarify", planner.secondPolicy)
        assertEquals("planner-output:conv-1:turn-past", planner.outputPayloadId)
        assertEquals(false, planner.validated)
    }

    @Test
    fun `transcript does not overwrite active turn planner state owned by SSE`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.applyEvent(
            SSEEvent(
                type = "turn_started",
                conversationId = "conv-1",
                turnId = "turn-live"
            )
        )
        tracker.applyEvent(
            SSEEvent(
                type = "planner.failed",
                conversationId = "conv-1",
                turnId = "turn-live",
                plannerTrigger = "exploratory_strategy",
                plannerAttempt = 2,
                plannerSecondPolicy = "block"
            )
        )

        tracker.reconcileTranscript(
            listOf(
                TurnState(
                    turnId = "turn-live",
                    status = "running",
                    planner = PlannerState(
                        status = "selected",
                        trigger = "low_confidence",
                        attempt = 1
                    )
                )
            )
        )

        val planner = tracker.snapshot().plannerByTurnId["turn-live"]
        assertNotNull(planner)
        assertEquals("failed", planner.status)
        assertEquals("exploratory_strategy", planner.trigger)
        assertEquals(2, planner.attempt)
        assertEquals("block", planner.secondPolicy)
    }

    @Test
    fun `usage event updates cumulative stream usage`() {
        val tracker = ConversationStreamTracker("conv-1")

        tracker.applyEvent(
            SSEEvent(
                type = "usage",
                conversationId = "conv-1",
                usageInputTokens = 120,
                usageOutputTokens = 30,
                usageEmbeddingTokens = 5,
                usageTotalTokens = 155
            )
        )

        assertEquals(120, tracker.snapshot().usage?.inputTokens)
        assertEquals(30, tracker.snapshot().usage?.outputTokens)
        assertEquals(5, tracker.snapshot().usage?.embeddingTokens)
        assertEquals(155, tracker.snapshot().usage?.totalTokens)
    }
}
