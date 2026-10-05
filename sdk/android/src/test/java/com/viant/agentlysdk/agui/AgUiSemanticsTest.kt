package com.viant.agentlysdk.agui
import kotlinx.serialization.json.*
import java.io.File
import kotlin.test.*
class AgUiSemanticsTest {
    @Test fun all35SharedReducerSemanticCases(){
        val cases=javaClass.getResourceAsStream("/agui/reducer-semantics.json")!!.use{Json.parseToJsonElement(it.bufferedReader().readText()).jsonArray};assertEquals(35,cases.size)
        val failures=mutableListOf<String>()
        for(rawFixture in cases){
            val fixture=rawFixture.obj();val name=fixture.requiredString("name");val accepted=fixture.getValue("accepted").jsonPrimitive.boolean
            val start=fixture.getValue("events").jsonArray.firstOrNull{it.obj().string("type")=="RUN_STARTED"}?.obj()
            val input=AgUiRunInput.create(start?.requiredString("threadId") ?: "t",start?.requiredString("runId") ?: "r",(fixture["initialMessages"] as? JsonArray).orEmpty().map{AgUiMessage(it.obj())},state=fixture["initialState"])
            val store=AgUiStore(input);val normalized=mutableListOf<JsonElement>();var failure:Throwable?=null
            try{for(raw in fixture.getValue("events").jsonArray)normalized+=store.receive(AgUiEvent(raw.obj())).map{it.value};normalized+=store.finish().map{it.value}}catch(error:Throwable){failure=error}
            if(!accepted){if(failure==null)failures+="Expected rejection: $name";continue}
            if(failure!=null){failures+="Accepted reference case $name failed: ${failure.message}";continue}
            if(JsonArray(store.messages.map{it.value})!=fixture["expectedMessages"])failures+="Messages $name: actual=${JsonArray(store.messages.map{it.value})} expected=${fixture["expectedMessages"]}"
            if(store.state!=fixture["expectedState"])failures+="State $name"
            if(JsonArray(normalized)!=fixture["normalized"])failures+="Normalization $name: actual=${JsonArray(normalized)} expected=${fixture["normalized"]}"
        }
        assertTrue(failures.isEmpty(),failures.joinToString("\n"))
    }
    @Test fun semanticMirrorMatchesCanonical(){
        val actual=javaClass.getResourceAsStream("/agui/reducer-semantics.json")!!.use{it.readBytes()}
        val canonical=File("../../protocol/agui/testdata/reducer-semantics.json").readBytes();assertContentEquals(canonical,actual)
    }
}

class AgUiIntegerSemanticsTest {
    @Test fun sharedIntegerSchemaSemanticsAndOpaqueNumberTokens(){
        val cases=javaClass.getResourceAsStream("/agui/integer-semantics.json")!!.use{Json.parseToJsonElement(it.bufferedReader().readText()).jsonArray};assertEquals(66,cases.size)
        for(rawFixture in cases){val fixture=rawFixture.obj();val name=fixture.requiredString("name");val value=Json.parseToJsonElement(fixture.requiredString("raw"))
            if(!fixture.getValue("accepted").jsonPrimitive.boolean){assertFailsWith<AgUiProtocolException>(name){AgUiSchema.validate(value,fixture.requiredString("definition"))};continue}
            AgUiSchema.validate(value,fixture.requiredString("definition"));var probe=value
            for(key in fixture.getValue("probePath").jsonArray)probe=probe.obj().getValue(key.jsonPrimitive.content)
            assertEquals(fixture.requiredString("expectedToken"),probe.jsonPrimitive.content,"Raw token: $name")
        }
    }
    @Test fun integerMirrorMatchesCanonical(){
        val actual=javaClass.getResourceAsStream("/agui/integer-semantics.json")!!.use{it.readBytes()}
        assertContentEquals(File("../../protocol/agui/testdata/integer-semantics.json").readBytes(),actual)
    }
}
