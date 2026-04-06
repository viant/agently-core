package com.viant.agentlysdk

import com.viant.forgeandroid.runtime.EndpointConfig
import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

class AgentlyClientTest {
    private val server = MockWebServer()

    @AfterTest
    fun tearDown() {
        server.shutdown()
    }

    @Test
    fun `getWorkspaceMetadata unwraps data envelope and default fallbacks`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "data": {
                    "workspaceRoot": "/tmp/workspace",
                    "defaults": {
                      "agent": "coder",
                      "model": "gpt-5.4",
                      "embedder": "openai_text"
                    }
                  }
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.getWorkspaceMetadata()

        assertEquals("/tmp/workspace", result.workspaceRoot)
        assertEquals("coder", result.defaultAgent)
        assertEquals("gpt-5.4", result.defaultModel)
        assertEquals("openai_text", result.defaultEmbedder)
        assertEquals("/v1/workspace/metadata", server.takeRequest().path)
    }

    @Test
    fun `listPendingToolApprovals accepts bare array response`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                [
                  {
                    "id": "approval-1",
                    "conversationId": "conv-1",
                    "toolName": "shell.exec",
                    "status": "pending"
                  }
                ]
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.listPendingToolApprovals(
            ListPendingToolApprovalsInput(conversationId = "conv-1")
        )

        assertEquals(1, result.size)
        assertEquals("approval-1", result.first().id)
        assertEquals("shell.exec", result.first().toolName)
        assertTrue(server.takeRequest().path!!.contains("/v1/tool-approvals/pending"))
    }

    @Test
    fun `listPendingToolApprovals accepts wrapped object response`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "data": [
                    {
                      "id": "approval-2",
                      "conversationId": "conv-2",
                      "toolName": "browser.open",
                      "status": "pending"
                    }
                  ]
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.listPendingToolApprovals(
            ListPendingToolApprovalsInput(conversationId = "conv-2")
        )

        assertEquals(1, result.size)
        assertEquals("approval-2", result.first().id)
        assertEquals("browser.open", result.first().toolName)
    }

    @Test
    fun `listPendingToolApprovalsPage decodes rows and pagination metadata`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "rows": [
                    {
                      "id": "approval-3",
                      "conversationId": "conv-3",
                      "toolName": "system.exec",
                      "status": "pending"
                    }
                  ],
                  "total": 11,
                  "offset": 5,
                  "limit": 5,
                  "hasMore": true
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.listPendingToolApprovalsPage(
            ListPendingToolApprovalsInput(conversationId = "conv-3", status = "pending", limit = 5, offset = 5)
        )

        assertEquals(1, result.rows.size)
        assertEquals("approval-3", result.rows.first().id)
        assertEquals(11, result.total)
        assertEquals(5, result.offset)
        assertEquals(5, result.limit)
        assertEquals(true, result.hasMore)
        val path = server.takeRequest().path.orEmpty()
        assertTrue(path.contains("conversationId=conv-3"))
        assertTrue(path.contains("status=pending"))
        assertTrue(path.contains("limit=5"))
        assertTrue(path.contains("offset=5"))
    }

    @Test
    fun `uploadFile sends multipart form data`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "ID": "payload-1",
                  "URI": "conversation://conv-1/file-1"
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.uploadFile(
            UploadFileInput(
                conversationId = "conv-1",
                name = "fixture.txt",
                contentType = "text/plain",
                data = "hello from test".encodeToByteArray()
            )
        )

        assertEquals("payload-1", result.id)
        assertEquals("conversation://conv-1/file-1", result.uri)
        val request = server.takeRequest()
        assertEquals("/v1/files", request.path)
        assertEquals("POST", request.method)
        val body = request.body.readUtf8()
        assertNotNull(request.getHeader("Content-Type"))
        assertTrue(request.getHeader("Content-Type")!!.contains("multipart/form-data"))
        assertTrue(body.contains("name=\"conversationId\""))
        assertTrue(body.contains("conv-1"))
        assertTrue(body.contains("name=\"file\""))
        assertTrue(body.contains("fixture.txt"))
        assertTrue(body.contains("hello from test"))
    }

    @Test
    fun `createConversation decodes PascalCase fields`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "Id": "conv-1",
                  "AgentId": "coder",
                  "Title": "Android QA",
                  "ConversationParentId": "parent-1",
                  "ConversationParentTurnId": "turn-9",
                  "Shareable": 1
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.createConversation(
            CreateConversationInput(
                agentId = "coder",
                title = "Android QA",
                parentConversationId = "parent-1",
                parentTurnId = "turn-9"
            )
        )

        assertEquals("conv-1", result.id)
        assertEquals("coder", result.agentId)
        assertEquals("Android QA", result.title)
        assertEquals("parent-1", result.conversationParentId)
        assertEquals("turn-9", result.conversationParentTurnId)
        assertEquals(1, result.shareable)
        val request = server.takeRequest()
        assertEquals("/v1/conversations", request.path)
        assertEquals("POST", request.method)
        assertTrue(request.body.readUtf8().contains("\"parentConversationId\":\"parent-1\""))
    }

    @Test
    fun `downloadFile returns body content type and inferred filename`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "text/plain")
                .setHeader("Content-Disposition", "attachment; filename=\"artifact.txt\"")
                .setBody("downloaded artifact")
        )
        server.start()
        val client = client()

        val result = client.downloadFile("conv-1", "file-9")

        assertEquals("artifact.txt", result.name)
        assertEquals("text/plain", result.contentType)
        assertEquals("downloaded artifact", result.data.toString(Charsets.UTF_8))
        val request = server.takeRequest()
        assertTrue(request.path!!.contains("/v1/files/file-9"))
        assertTrue(request.path!!.contains("conversationId=conv-1"))
        assertTrue(request.path!!.contains("raw=1"))
    }

    @Test
    fun `oauthInitiate decodes configured and error fields`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "status": "error",
                  "message": "oauth client not configured",
                  "provider": "google"
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.oauthInitiate()

        assertEquals("error", result.status)
        assertEquals("oauth client not configured", result.message)
        assertEquals("google", result.provider)
        val request = server.takeRequest()
        assertEquals("/v1/api/auth/oauth/initiate", request.path)
        assertEquals("POST", request.method)
    }

    @Test
    fun `listConversations builds query parameters`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "rows": [],
                  "hasMore": false
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        client.listConversations(
            ListConversationsInput(
                agentId = "coder",
                parentId = "parent-1",
                parentTurnId = "turn-2",
                excludeScheduled = true,
                query = "android qa",
                status = "active",
                page = PageInput(limit = 25, cursor = "cursor-1", direction = "next")
            )
        )

        val path = server.takeRequest().path.orEmpty()
        assertTrue(path.startsWith("/v1/conversations?"))
        assertTrue(path.contains("agentId=coder"))
        assertTrue(path.contains("parentId=parent-1"))
        assertTrue(path.contains("parentTurnId=turn-2"))
        assertTrue(path.contains("excludeScheduled=true"))
        assertTrue(path.contains("q=android+qa"))
        assertTrue(path.contains("status=active"))
        assertTrue(path.contains("limit=25"))
        assertTrue(path.contains("cursor=cursor-1"))
        assertTrue(path.contains("direction=next"))
    }

    @Test
    fun `listConversations decodes PascalCase page envelope`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "Rows": [
                    {
                      "Id": "conv-9",
                      "AgentId": "chatter",
                      "Title": "Mobile QA",
                      "LastActivity": "2026-04-06T08:51:45.014809-07:00",
                      "CreatedAt": "2026-04-06T08:51:42.660325-07:00"
                    }
                  ],
                  "NextCursor": "conv-8",
                  "PrevCursor": "conv-10",
                  "HasMore": true
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.listConversations()

        assertEquals(1, result.rows.size)
        assertEquals("conv-9", result.rows.first().id)
        assertEquals("chatter", result.rows.first().agentId)
        assertEquals("Mobile QA", result.rows.first().title)
        assertEquals("conv-8", result.nextCursor)
        assertEquals("conv-10", result.prevCursor)
        assertTrue(result.hasMore)
    }

    @Test
    fun `listLinkedConversations decodes PascalCase page envelope`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "Rows": [
                    {
                      "conversationId": "child-1",
                      "parentConversationId": "parent-1",
                      "title": "Linked child",
                      "status": "done"
                    }
                  ],
                  "NextCursor": "cursor-next",
                  "PrevCursor": "cursor-prev",
                  "HasMore": false
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.listLinkedConversations(
            ListLinkedConversationsInput(parentConversationId = "parent-1")
        )

        assertEquals(1, result.rows.size)
        assertEquals("child-1", result.rows.first().conversationId)
        assertEquals("parent-1", result.rows.first().parentConversationId)
        assertEquals("cursor-next", result.nextCursor)
        assertEquals("cursor-prev", result.prevCursor)
        assertEquals(false, result.hasMore)
    }

    @Test
    fun `query posts payload and decodes response`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "conversationId": "conv-7",
                  "content": "Hello from backend",
                  "model": "gpt-5.4",
                  "messageId": "msg-7",
                  "warnings": ["warn-1"]
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.query(
            QueryInput(
                conversationId = "conv-7",
                agentId = "coder",
                query = "hello",
                context = mapOf("mode" to buildJsonObject { put("kind", "qa") })
            )
        )

        assertEquals("conv-7", result.conversationId)
        assertEquals("Hello from backend", result.content)
        assertEquals("gpt-5.4", result.model)
        assertEquals("msg-7", result.messageId)
        assertEquals(listOf("warn-1"), result.warnings)
        val request = server.takeRequest()
        assertEquals("/v1/agent/query", request.path)
        assertEquals("POST", request.method)
        val body = request.body.readUtf8()
        assertTrue(body.contains("\"conversationId\":\"conv-7\""))
        assertTrue(body.contains("\"agentId\":\"coder\""))
        assertTrue(body.contains("\"query\":\"hello\""))
    }

    @Test
    fun `downloadGeneratedFile uses expected path and headers`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "application/json")
                .setHeader("Content-Disposition", "attachment; filename=\"artifact.json\"")
                .setBody("""{"ok":true}""")
        )
        server.start()
        val client = client()

        val result = client.downloadGeneratedFile("gen-1")

        assertEquals("artifact.json", result.name)
        assertEquals("application/json", result.contentType)
        assertEquals("""{"ok":true}""", result.data.toString(Charsets.UTF_8))
        val request = server.takeRequest()
        assertEquals("/v1/api/generated-files/gen-1/download", request.path)
        assertEquals("GET", request.method)
    }

    private fun client(): AgentlyClient {
        return AgentlyClient(
            endpoints = mapOf(
                "appAPI" to EndpointConfig(baseUrl = server.url("/").toString().trimEnd('/'))
            )
        )
    }
}
