package agui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/streaming"
)

func TestSeededPendingToolResumeInteroperatesWithUpstreamHTTPClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is unavailable")
	}
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	module := filepath.Join(root, "sdk", "ts", "node_modules", "@ag-ui", "client", "dist", "index.mjs")
	if _, err = os.Stat(module); err != nil {
		t.Skip("pinned upstream TypeScript dependencies are unavailable")
	}
	history := []json.RawMessage{json.RawMessage(`{"id":"user","role":"user","content":"question"}`)}
	first := NewTranslator("thread", "first")
	require.NoError(t, first.SetNativeIdentity("same/logical-turn"))
	require.NoError(t, first.SeedMessages(history))
	firstEvents := first.CompleteText("assistant", "draft")
	firstEvents = append(firstEvents, first.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, TurnID: "same/logical-turn", ToolCallID: "call", ToolName: "lookup", AssistantMessageID: "assistant", Arguments: map[string]any{"q": "question"}})...)
	firstEvents = append(firstEvents, first.Finish("success")...)
	alias := ProtocolToolCallID("same/logical-turn", "call")
	second := NewTranslator("thread", "second")
	require.NoError(t, second.SetNativeIdentity("same/logical-turn"))
	require.NoError(t, second.SeedMessages(first.messages))
	completion := &streaming.Event{Type: streaming.EventTypeToolCallCompleted, TurnID: "same/logical-turn", ToolCallID: "call", ToolMessageID: "result", Status: "completed", Content: "answer", Arguments: map[string]any{"q": "question"}}
	// The approval decision's authoritative receipt can edit arguments before
	// the native completion arrives. Its original argument snapshot must not
	// overwrite the canonical edit or stream another argument fragment.
	edited := append([]json.RawMessage(nil), first.messages...)
	for i, raw := range edited {
		var message map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &message))
		if fieldString(message, "id") != "assistant" {
			continue
		}
		var calls []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(message["toolCalls"], &calls))
		calls[0]["function"] = rawJSON(map[string]any{"name": "lookup", "arguments": `{"q":"approved edit"}`})
		message["toolCalls"] = rawJSON(calls)
		edited[i] = rawJSON(message)
	}
	canonical := standardEvent(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": edited})
	secondEvents := second.Start()
	require.NoError(t, second.captureStandard(canonical.Standard, true))
	secondEvents = append(secondEvents, canonical)
	secondEvents = append(secondEvents, second.Translate(completion)...)
	require.Equal(t, []string{"RUN_STARTED", "MESSAGES_SNAPSHOT", "TOOL_CALL_RESULT", "ACTIVITY_SNAPSHOT"}, kinds(secondEvents))
	assertToolExecutionPresentation(t, secondEvents[3], alias, "completed")
	require.Empty(t, second.Translate(completion), "repeated native completion must emit nothing")
	secondEvents = append(secondEvents, second.Finish("success")...)
	third := NewTranslator("thread", "third")
	require.NoError(t, third.SetNativeIdentity("same/logical-turn"))
	require.NoError(t, third.SeedMessages(second.messages))
	thirdEvents := third.Translate(completion)
	require.Equal(t, []string{"RUN_STARTED"}, kinds(thirdEvents), "receipt in prior run deduplicates callback")
	thirdEvents = append(thirdEvents, third.CompleteText("assistant", "replacement")...)
	thirdEvents = append(thirdEvents, third.Finish("success")...)
	fixture := struct {
		Initial []json.RawMessage   `json:"initial"`
		Runs    [][]json.RawMessage `json:"runs"`
		Alias   string              `json:"alias"`
	}{history, [][]json.RawMessage{checkedEvents(t, firstEvents), checkedEvents(t, secondEvents), checkedEvents(t, thirdEvents)}, alias}
	data, err := json.Marshal(fixture)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "fixture.json")
	require.NoError(t, os.WriteFile(path, data, 0600))
	script := `import http from 'node:http';
import fs from 'node:fs';
import {pathToFileURL} from 'node:url';
const {HttpAgent}=await import(pathToFileURL(process.argv[1]).href);
const fixture=JSON.parse(fs.readFileSync(process.argv[2],'utf8'));
let count=0;
const server=http.createServer(async(req,res)=>{for await(const chunk of req){};res.writeHead(200,{'Content-Type':'text/event-stream'});for(const event of fixture.runs[count++])res.write('data: '+JSON.stringify(event)+'\n\n');res.end();});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
try {
 const agent=new HttpAgent({url:'http://127.0.0.1:'+server.address().port+'/run',threadId:'thread',initialMessages:fixture.initial});
 let starts=0,args=0,results=0;
 agent.subscribe({onEvent:({event})=>{if(event.type==='TOOL_CALL_START')starts++;if(event.type==='TOOL_CALL_ARGS')args++;if(event.type==='TOOL_CALL_RESULT')results++;}});
 for(const runId of ['first','second','third']) {
  await agent.runAgent({runId});
  const calls=agent.messages.flatMap(m=>m.role==='assistant'?m.toolCalls??[]:[]);
  if(calls.length!==1||calls[0].id!==fixture.alias||calls[0].function.arguments!==(runId==='first'?'{"q":"question"}':'{"q":"approved edit"}'))throw Error('historical tool call or arguments duplicated');
  const receipts=agent.messages.filter(m=>m.role==='tool');
  if(receipts.length!==(runId==='first'?0:1))throw Error('receipt repeated or lost');
  if(receipts.length&&receipts[0].toolCallId!==fixture.alias)throw Error('receipt alias differs');
 }
 if(starts!==1||args!==1||results!==1)throw Error('repeated historical tool wire events '+JSON.stringify({starts,args,results}));
 if(agent.messages.find(m=>m.id==='assistant').content!=='replacement')throw Error('canonical replacement lost');
 console.log('PASS same logical turn resumed completion, aliases, edited args, callback dedup, canonical snapshot');
} finally {await new Promise(resolve=>server.close(resolve));}`
	command := exec.Command(node, "--input-type=module", "-e", script, module, path)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	t.Log(string(output))
}
