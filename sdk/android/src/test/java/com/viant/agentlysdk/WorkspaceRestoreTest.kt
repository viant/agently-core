package com.viant.agentlysdk

import com.viant.agentlysdk.stream.ConversationStreamSnapshot
import com.viant.agentlysdk.stream.LiveExecutionGroup
import com.viant.agentlysdk.stream.LiveToolStepState
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import kotlinx.serialization.json.JsonPrimitive

class WorkspaceRestoreTest {

    @Test
    fun `deriveHostedWorkspaceRestoreState restores hosted window from latest transcript turn`() {
        val state = ConversationStateResponse(
            conversation = ConversationState(
                conversationId = "conv-1",
                turns = listOf(
                    TurnState(
                        turnId = "turn-1",
                        execution = ExecutionState(
                            pages = listOf(
                                ExecutionPageState(
                                    pageId = "page-1",
                                    toolSteps = listOf(
                                        ToolStepState(
                                            toolCallId = "tool-1",
                                            toolName = "ui/view:open",
                                            status = "completed",
                                            requestPayload = buildJsonObject {
                                                put("id", "reportWindow")
                                            },
                                            responsePayload = buildJsonObject {
                                                put("windowId", "reportWindow__conv-1")
                                                put("conversationId", "conv-1")
                                                put("windowKey", "reportWindow")
                                                put("windowTitle", "Report Review")
                                                put("presentation", "hosted")
                                                put("region", "chat.top")
                                                put("parentKey", "chat/new")
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

        val restore = deriveHostedWorkspaceRestoreState(state)

        assertEquals("reportWindow__conv-1", restore?.selectedWindowId)
        assertEquals("reportWindow", restore?.windows?.firstOrNull()?.windowKey)
    }

    @Test
    fun `deriveHostedWorkspaceRestoreState does not require app placement fields`() {
        val state = ConversationStateResponse(
            conversation = ConversationState(
                conversationId = "conv-1",
                turns = listOf(
                    TurnState(
                        turnId = "turn-1",
                        execution = ExecutionState(
                            pages = listOf(
                                ExecutionPageState(
                                    pageId = "page-1",
                                    toolSteps = listOf(
                                        ToolStepState(
                                            toolCallId = "tool-1",
                                            toolName = "ui/view/open",
                                            status = "completed",
                                            responsePayload = buildJsonObject {
                                                put("windowId", "generic__conv-1")
                                                put("windowKey", "generic-report")
                                                put("windowTitle", "Generic Report")
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

        val restore = deriveHostedWorkspaceRestoreState(state)

        assertEquals("generic__conv-1", restore?.selectedWindowId)
        assertEquals("generic-report", restore?.windows?.firstOrNull()?.windowKey)
    }

    @Test
    fun `deriveHostedWorkspaceRestoreState folds set form data into restored window form`() {
        val state = ConversationStateResponse(
            conversation = ConversationState(
                conversationId = "conv-1",
                turns = listOf(
                    TurnState(
                        turnId = "turn-1",
                        execution = ExecutionState(
                            pages = listOf(
                                ExecutionPageState(
                                    pageId = "page-1",
                                    toolSteps = listOf(
                                        ToolStepState(
                                            toolCallId = "tool-open",
                                            toolName = "ui/view/open",
                                            status = "completed",
                                            responsePayload = buildJsonObject {
                                                put("windowId", "reportBuilder__conv-1")
                                                put("conversationId", "conv-1")
                                                put("windowKey", "reportBuilder")
                                                put("windowTitle", "Report Builder")
                                                put(
                                                    "windowForm",
                                                    buildJsonObject {
                                                        put(
                                                            "prefill",
                                                            buildJsonObject {
                                                                put("accountId", 7)
                                                            }
                                                        )
                                                    }
                                                )
                                            }
                                        ),
                                        ToolStepState(
                                            toolCallId = "tool-form",
                                            toolName = "ui/window:setFormData",
                                            status = "completed",
                                            requestPayload = buildJsonObject {
                                                put("windowId", "reportBuilder__conv-1")
                                                put(
                                                    "values",
                                                    buildJsonObject {
                                                        put(
                                                            "prefill",
                                                            buildJsonObject {
                                                                put("recordId", 123)
                                                            }
                                                        )
                                                    }
                                                )
                                            },
                                            responsePayload = buildJsonObject {
                                                put("ok", true)
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

        val restore = deriveHostedWorkspaceRestoreState(state)
        val prefill = restore?.windows?.firstOrNull()?.windowForm
            ?.get("prefill")
            ?.jsonObject

        assertEquals(7, prefill?.get("accountId")?.jsonPrimitive?.int)
        assertEquals(123, prefill?.get("recordId")?.jsonPrimitive?.int)
    }

    @Test
    fun `deriveHostedWorkspaceRestoreState does not fall back past latest transcript turn`() {
        val oldHostedTurn = TurnState(
            turnId = "turn-1",
            execution = ExecutionState(
                pages = listOf(
                    ExecutionPageState(
                        pageId = "page-1",
                        toolSteps = listOf(
                            ToolStepState(
                                toolCallId = "tool-1",
                                toolName = "ui/window/list",
                                status = "completed",
                                responsePayload = buildJsonObject {
                                    put(
                                        "items",
                                        kotlinx.serialization.json.buildJsonArray {
                                            add(
                                                buildJsonObject {
                                                    put("windowId", "legacy__conv-1")
                                                    put("conversationId", "conv-1")
                                                    put("windowKey", "report")
                                                    put("presentation", "hosted")
                                                    put("region", "chat.top")
                                                    put("parentKey", "chat/new")
                                                }
                                            )
                                        }
                                    )
                                }
                            )
                        )
                    )
                )
            )
        )
        val latestNonHostedTurn = TurnState(
            turnId = "turn-2",
            execution = ExecutionState(
                pages = listOf(
                    ExecutionPageState(
                        pageId = "page-2",
                        toolSteps = listOf(
                            ToolStepState(
                                toolCallId = "tool-2",
                                toolName = "message/reply",
                                status = "completed"
                            )
                        )
                    )
                )
            )
        )

        assertNull(
            deriveHostedWorkspaceRestoreState(
                ConversationStateResponse(
                    conversation = ConversationState(
                        conversationId = "conv-1",
                        turns = listOf(oldHostedTurn, latestNonHostedTurn)
                    )
                )
            )
        )
    }

    @Test
    fun `deriveHostedWorkspaceRestoreState restores hosted window from live stream snapshot`() {
        val snapshot = ConversationStreamSnapshot(
            conversationId = "conv-1",
            activeTurnId = "turn-1",
            feeds = emptyList(),
            pendingElicitation = null,
            bufferedMessages = emptyList(),
            liveExecutionGroupsById = mapOf(
                "assistant-1" to LiveExecutionGroup(
                    pageId = "page-1",
                    assistantMessageId = "assistant-1",
                    turnId = "turn-1",
                    toolSteps = listOf(
                        LiveToolStepState(
                            toolCallId = "tool-1",
                            toolName = "ui/view/open",
                            status = "completed",
                            requestPayload = buildJsonObject {
                                put("id", "reportWindow")
                            },
                            responsePayload = buildJsonObject {
                                put("windowId", "reportWindow__conv-1")
                                put("conversationId", "conv-1")
                                put("windowKey", "reportWindow")
                                put("windowTitle", "Report Review")
                                put("presentation", "hosted")
                                put("region", "chat.top")
                                put("parentKey", "chat/new")
                            }
                        )
                    )
                )
            )
        )

        val restore = deriveHostedWorkspaceRestoreState(snapshot)

        assertEquals("reportWindow__conv-1", restore?.selectedWindowId)
        assertEquals("Report Review", restore?.windows?.firstOrNull()?.windowTitle)
    }

    @Test
    fun `deriveHostedWorkspaceRestoreState ignores live groups without active turn`() {
        val snapshot = ConversationStreamSnapshot(
            conversationId = "conv-1",
            activeTurnId = null,
            feeds = emptyList(),
            pendingElicitation = null,
            bufferedMessages = emptyList(),
            liveExecutionGroupsById = mapOf(
                "assistant-old" to LiveExecutionGroup(
                    pageId = "page-old",
                    assistantMessageId = "assistant-old",
                    turnId = "turn-old",
                    toolSteps = listOf(
                        LiveToolStepState(
                            toolCallId = "tool-old",
                            toolName = "ui/view/open",
                            status = "completed",
                            responsePayload = buildJsonObject {
                                put("windowId", "old__conv-1")
                                put("windowKey", "old-report")
                            }
                        )
                    )
                )
            )
        )

        assertNull(deriveHostedWorkspaceRestoreState(snapshot))
    }

    @Test
    fun `deriveHostedWorkspaceRestoreState restores hosted line window from live transcript payload envelope`() {
        val content = """
            {"clientId":"ios-ui-123","conversationId":"conv-1","items":[{"conversationId":"conv-1","parameters":{"AudienceId":[7289845]},"parentKey":"chat/new","presentation":"hosted","region":"chat.top","windowId":"line_3866014773__conv-1","windowKey":"line","windowTitle":"Line Summary","workspaceMinHeight":500,"workspaceSharePct":72}],"ok":true,"parameters":{"AudienceId":[7289845]},"parentKey":"chat/new","presentation":"hosted","region":"chat.top","selectedWindowId":"line_3866014773__conv-1","windowId":"line_3866014773__conv-1","windowKey":"line","windowTitle":"Line Summary","workspaceMinHeight":500,"workspaceSharePct":72}
        """.trimIndent()

        val state = ConversationStateResponse(
            conversation = ConversationState(
                conversationId = "conv-1",
                turns = listOf(
                    TurnState(
                        turnId = "turn-1",
                        execution = ExecutionState(
                            pages = listOf(
                                ExecutionPageState(
                                    pageId = "page-1",
                                    toolSteps = listOf(
                                        ToolStepState(
                                            toolCallId = "tool-1",
                                            toolName = "ui/view/open",
                                            status = "completed",
                                            content = content,
                                            responsePayload = buildJsonObject {
                                                put("Id", "payload-1")
                                                put("InlineBody", JsonPrimitive("H4sIAAAAAAAA/opaque"))
                                                put("Compression", "gzip")
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

        val restore = deriveHostedWorkspaceRestoreState(state)

        assertEquals("line_3866014773__conv-1", restore?.selectedWindowId)
        assertEquals("line", restore?.windows?.firstOrNull()?.windowKey)
        assertEquals("Line Summary", restore?.windows?.firstOrNull()?.windowTitle)
    }
}
