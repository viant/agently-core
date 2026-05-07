package com.viant.agentlysdk.stream

import com.viant.agentlysdk.PlannerState
import com.viant.agentlysdk.TurnState
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlinx.serialization.json.buildJsonObject
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
