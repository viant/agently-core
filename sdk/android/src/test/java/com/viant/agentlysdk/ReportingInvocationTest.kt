package com.viant.agentlysdk

import kotlinx.serialization.json.*
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class ReportingInvocationTest {
    @Test fun beginRetainsAdmittedBuilderAndScope() {
        val params = Json.parseToJsonElement("""{"filters":{"adOrderId":[2659534]}}""")
        val input = BeginReportRunInput("request", "conversation", "prompt", "delivery", "preset", "preset", "source", params, params, reportAdmissionRef = "opaque ref")
        val encoded = Json.encodeToJsonElement(input).jsonObject
        assertEquals(JsonPrimitive("delivery"), encoded["builderRef"])
        assertEquals(params, encoded["requestedParams"])
        assertEquals(params, encoded["effectiveParams"])
        assertEquals(JsonPrimitive("source"), encoded["sourceId"])
        assertEquals(JsonPrimitive("opaque ref"), encoded["reportAdmissionRef"])
        assertNull(encoded["requestedParams"]?.jsonObject?.get("_agentlyForecastCommand"))
        assertNull(Json.encodeToJsonElement(BeginReportRunInput("old")).jsonObject["builderRef"])
    }
}
