package com.viant.agentlysdk.stream

import com.viant.agentlysdk.AssistantMessageState
import com.viant.agentlysdk.AssistantState
import com.viant.agentlysdk.ConversationState
import com.viant.agentlysdk.ConversationStateResponse
import com.viant.agentlysdk.ExecutionPageState
import com.viant.agentlysdk.ExecutionState
import com.viant.agentlysdk.ModelStepState
import com.viant.agentlysdk.PlannerState
import com.viant.agentlysdk.ToolStepState
import com.viant.agentlysdk.TurnMessageState
import com.viant.agentlysdk.TurnState
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

class ConversationStreamTrackerTest {

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
}
