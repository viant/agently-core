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
    fun `deriveHostedWorkspaceRestoreState restores hosted window from ui window open tool step`() {
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
                                            toolName = "ui/window/open",
                                            status = "completed",
                                            requestPayload = buildJsonObject {
                                                put("windowKey", "reportBuilder")
                                                put(
	                                                    "parameters",
	                                                    buildJsonObject {
	                                                        put("reportBuilderRef", "capacityBuilder")
	                                                    }
	                                                )
                                            },
                                            responsePayload = buildJsonObject {
                                                put("windowId", "reportBuilder__conv-1")
                                                put("conversationId", "conv-1")
                                                put("windowKey", "reportBuilder")
                                                put("windowTitle", "Capacity Builder")
                                                put("presentation", "hosted")
                                                put("region", "chat.top")
                                                put("parentKey", "chat/new")
                                                put("workspaceMinHeight", 500)
                                                put("workspaceSharePct", 72)
                                            }
                                        ),
                                        ToolStepState(
                                            toolCallId = "tool-prefill",
                                            toolName = "ui/window/setFormData",
                                            status = "completed",
                                            requestPayload = buildJsonObject {
                                                put("windowId", "reportBuilder__conv-1")
                                                put(
                                                    "values",
                                                    buildJsonObject {
                                                        put(
                                                            "prefill",
                                                            buildJsonObject {
                                                                put(
                                                                    "scope",
                                                                    buildJsonObject {
                                                                        put("targetKey", "record:12345")
                                                                    }
                                                                )
                                                            }
                                                        )
                                                    }
                                                )
                                            },
                                            responsePayload = buildJsonObject {
                                                put("windowId", "reportBuilder__conv-1")
                                                put(
                                                    "windowForm",
                                                    buildJsonObject {
                                                        put("reportBuilderRef", "capacityBuilder")
                                                        put(
                                                            "prefill",
                                                            buildJsonObject {
                                                                put(
                                                                    "scope",
                                                                    buildJsonObject {
                                                                        put("targetKey", "record:12345")
                                                                    }
                                                                )
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
                )
            )
        )

        val restore = deriveHostedWorkspaceRestoreState(state)
        val window = restore?.windows?.singleOrNull()
        val scope = window?.windowForm
            ?.get("prefill")
            ?.jsonObject
            ?.get("scope")
            ?.jsonObject

        assertEquals("reportBuilder__conv-1", restore?.selectedWindowId)
        assertEquals("reportBuilder", window?.windowKey)
        assertEquals("Capacity Builder", window?.windowTitle)
        assertEquals(500, window?.workspaceMinHeight)
        assertEquals(72, window?.workspaceSharePct)
        assertEquals(JsonPrimitive("capacityBuilder"), window?.windowForm?.get("reportBuilderRef"))
        assertEquals(JsonPrimitive("record:12345"), scope?.get("targetKey"))
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
                                                put("windowKey", "reportBuilder")
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
    fun `deriveHostedWorkspaceRestoreState restores hosted order from latest ui context`() {
        val context = buildJsonObject {
            put("focusedWindowId", "chat/new")
            put("selected", buildJsonObject { put("windowId", "chat/new") })
            put("windows", kotlinx.serialization.json.buildJsonArray {
                add(buildJsonObject {
                    put("window", buildJsonObject {
                        put("windowId", "chat/new")
                        put("windowKey", "chat/new")
                    })
                })
                add(buildJsonObject {
                    put("window", buildJsonObject {
                        put("windowId", "order__conv-1")
                        put("conversationId", "conv-1")
                        put("windowKey", "order")
                        put("windowTitle", "Frisco - Display (2688386)")
                        put("presentation", "hosted")
                        put("region", "chat.top")
                        put("parentKey", "chat/new")
                        put("parameters", buildJsonObject {
                            put("AdOrderId", kotlinx.serialization.json.buildJsonArray { add(JsonPrimitive(2688386)) })
                        })
                        put("windowForm", buildJsonObject {
                            put("periodView", "today")
                            put("granularity", "hour")
                        })
                    })
                })
            })
        }
        val state = ConversationStateResponse(
            conversation = ConversationState(
                conversationId = "conv-1",
                turns = listOf(
                    TurnState(
                        turnId = "turn-2",
                        execution = ExecutionState(
                            pages = listOf(
                                ExecutionPageState(
                                    pageId = "page-2",
                                    toolSteps = listOf(
                                        ToolStepState(
                                            toolCallId = "context-1",
                                            toolName = "ui/context/get",
                                            status = "completed",
                                            content = context.toString()
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

        assertEquals("chat/new", restore?.selectedWindowId)
        assertEquals("order", restore?.windows?.lastOrNull()?.windowKey)
        assertEquals("today", restore?.windows?.lastOrNull()?.windowForm?.get("periodView")?.jsonPrimitive?.content)
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
    fun `deriveHostedWorkspaceRestoreState falls back to durable state after live turn completes`() {
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
        val completedSnapshotWithBufferedGroups = ConversationStreamSnapshot(
            conversationId = "conv-1",
            activeTurnId = null,
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
                            responsePayload = buildJsonObject {
                                put("windowId", "reportWindow__conv-1")
                                put("conversationId", "conv-1")
                                put("windowKey", "reportWindow")
                            }
                        )
                    )
                )
            )
        )

        val restore = deriveHostedWorkspaceRestoreState(state, completedSnapshotWithBufferedGroups)

        assertEquals("reportWindow__conv-1", restore?.selectedWindowId)
        assertEquals("reportWindow", restore?.windows?.firstOrNull()?.windowKey)
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
