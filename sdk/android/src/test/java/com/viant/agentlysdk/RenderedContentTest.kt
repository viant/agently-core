package com.viant.agentlysdk

import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertEquals

class RenderedContentTest {
    @Test
    fun `decodes progressive inline report assembly`() {
        val payload = """
            {
              "schemaVersion":"1",
              "parts":[{"kind":"forgeReport","report":{"version":1,"id":"brief","sequence":2,"mode":"start","payload":{"id":"brief"}}}],
              "reports":[{"scope":"campaign","id":"brief","grammar":"dashboard-v1","status":"committed","sequence":2,"source":{"title":"Delivery","blocks":[]},"dataSources":{"rows":{"version":2,"reportRef":"brief","sequence":1,"id":"rows","payload":[{"spend":12}]}}}]
            }
        """.trimIndent()

        val decoded = Json { ignoreUnknownKeys = true }.decodeFromString(RenderedContent.serializer(), payload)

        assertEquals("brief", decoded.parts.first().report?.id)
        assertEquals("dashboard-v1", decoded.reports.first().grammar)
        assertEquals("committed", decoded.reports.first().status)
        assertEquals("brief", decoded.reports.first().dataSources["rows"]?.reportRef)
    }
}
