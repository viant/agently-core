package com.viant.agentlysdk.stream

import com.viant.agentlysdk.*
import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertEquals

class CanonicalMessagesTest {
    @Test fun canonicalTaskMessagesAndAggregatesKeepExactIdentities() {
        val turn = TurnState(turnId="t", status="completed", messages=listOf(
            TurnMessageState("router","assistant","{\"classification\":true}",sequence=1,mode="router"),
            TurnMessageState("interim","assistant","Preliminary findings",sequence=2,mode="task"),
            TurnMessageState("final","assistant","Final report",sequence=4,mode="task")
        ),assistant=AssistantState(narration=AssistantMessageState("narration","Checking launch day"),final=AssistantMessageState("final","Final report")))
        assertEquals(listOf("interim","narration","final"),canonicalAssistantMessages(turn).map { it.messageId })
        val hydrated=assistantMessagesFromTurns(listOf(turn))
        assertEquals(listOf("interim","narration","final"),hydrated.map { it.id })
        assertEquals(1,hydrated.count { it.id=="final" })
        val sameBody=turn.copy(messages=listOf(TurnMessageState("a","assistant","same",mode="task"),TurnMessageState("b","assistant","same",mode="task")),assistant=null)
        assertEquals(listOf("a","b"),canonicalAssistantMessages(sameBody).map { it.messageId })
    }
    @Test fun repeatedCanonicalIdentityEnrichesAndBlankIdentityIsIgnored() {
        val turn = TurnState(turnId="t", messages=listOf(
            TurnMessageState("", "assistant", "invalid"),
            TurnMessageState("a", "assistant", "body", sequence=2, mode="task"),
            TurnMessageState("a", "assistant", null, status="completed")
        ))
        val messages = canonicalAssistantMessages(turn)
        assertEquals(1, messages.size)
        assertEquals("body", messages.single().content)
        assertEquals(2, messages.single().sequence)
        assertEquals("completed", messages.single().status)
    }

    @Test fun actualGoCanonicalizerModelInclusiveFixtureRetainsVisibleTimeline() {
        val raw = requireNotNull(javaClass.getResource("/canonical-native-timeline.json")).readText()
        val turn = Json { ignoreUnknownKeys = true }.decodeFromString<TurnState>(raw)
        assertEquals("user", turn.user?.messageId)
        val messages = canonicalAssistantMessages(turn)
        assertEquals(listOf("interim", "narration", "final"), messages.map { it.messageId })
        assertEquals(listOf("Preliminary findings", "Checking results", "Final report"), messages.map { it.content })
        assertEquals(3, turn.execution?.pages?.flatMap { it.modelSteps }?.size)
    }

}
