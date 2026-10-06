package agui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

func nativeAliasCall(tr *Translator, turn, message string) []Event {
	var events []Event
	events = append(events, tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallStarted, TurnID: turn, ToolCallID: "reused-call", ToolName: "lookup", AssistantMessageID: message, Arguments: map[string]any{"turn": turn}})...)
	events = append(events, tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, TurnID: turn, ToolCallID: "reused-call", ToolMessageID: "result-" + turn, Status: "completed", Content: "done"})...)
	return events
}
func TestProductionToolAliasesSurviveResumeAndAllowNativeReuse(t *testing.T) {
	tr := NewTranslator("thread", "run")
	require.NoError(t, tr.SetNativeIdentity("native/turn"))
	events := tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallDelta, TurnID: "native/turn", ToolCallID: "reused-call", ToolName: "lookup", AssistantMessageID: "assistant", Content: `{"q":`})
	alias := ProtocolToolCallID("native/turn", "reused-call")
	require.Equal(t, alias, events[len(events)-1].ToolCallID)
	restored, err := RestoreTranslator("thread", "run", checkedEvents(t, events))
	require.NoError(t, err)
	resumed := restored.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, TurnID: "native/turn", ToolCallID: "reused-call", Arguments: map[string]any{"q": "answer"}})
	require.Equal(t, []string{"TOOL_CALL_ARGS", "TOOL_CALL_END"}, kinds(resumed))
	require.Equal(t, alias, resumed[0].ToolCallID)
	require.NotEqual(t, alias, ProtocolToolCallID("another-turn", "reused-call"))
	require.NotEqual(t, ProtocolToolCallID("a/b", "c"), ProtocolToolCallID("a", "b/c"))
}

// This executes the actual pinned upstream HttpAgent over real HTTP. It catches
// ownership/ordering errors that schema-only producer fixture tests cannot.
func TestNativeRepeatedToolIDsInteroperateWithUpstreamHTTPClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is unavailable")
	}
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	module := filepath.Join(root, "sdk", "ts", "node_modules", "@ag-ui", "client", "dist", "index.mjs")
	if _, err = os.Stat(module); err != nil {
		t.Skip("pinned TypeScript upstream dependencies are unavailable")
	}
	history := []json.RawMessage{json.RawMessage(`{"id":"user","role":"user","content":"question"}`)}
	first := NewTranslator("thread", "first")
	require.NoError(t, first.SetNativeIdentity("turn-one"))
	require.NoError(t, first.SeedMessages(history))
	firstEvents := nativeAliasCall(first, "turn-one", "assistant-one")
	firstEvents = append(firstEvents, first.Finish("success")...)
	second := NewTranslator("thread", "second")
	require.NoError(t, second.SetNativeIdentity("turn-two"))
	require.NoError(t, second.SeedMessages(first.messages))
	secondEvents := nativeAliasCall(second, "turn-two", "assistant-two")
	for _, turn := range []string{"child-one", "child-two"} {
		inv := requestctx.Invocation{ID: turn, ConversationID: "reused-child-conversation", TurnID: turn, Name: "child", ParentConversationID: "thread", ParentTurnID: "turn-two"}
		secondEvents = append(secondEvents, second.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeTurnStarted, ConversationID: inv.ConversationID, TurnID: turn})...)
		secondEvents = append(secondEvents, second.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeToolCallStarted, ConversationID: inv.ConversationID, TurnID: turn, ToolCallID: "reused-call", ToolName: "lookup", AssistantMessageID: "assistant-" + turn, Arguments: map[string]any{"turn": turn}})...)
		secondEvents = append(secondEvents, second.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeToolCallCompleted, ConversationID: inv.ConversationID, TurnID: turn, ToolCallID: "reused-call", ToolMessageID: "result-" + turn, Status: "completed", Content: "done"})...)
		secondEvents = append(secondEvents, second.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeTurnCompleted, ConversationID: inv.ConversationID, TurnID: turn})...)
	}
	secondEvents = append(secondEvents, second.Finish("success")...)
	fixture := struct {
		Initial []json.RawMessage `json:"initial"`
		First   []json.RawMessage `json:"first"`
		Second  []json.RawMessage `json:"second"`
	}{Initial: history, First: checkedEvents(t, firstEvents), Second: checkedEvents(t, secondEvents)}
	bytes, err := json.Marshal(fixture)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "fixture.json")
	require.NoError(t, os.WriteFile(path, bytes, 0600))
	script := `import http from 'node:http';
import fs from 'node:fs';
import {pathToFileURL} from 'node:url';
const {HttpAgent}=await import(pathToFileURL(process.argv[1]).href);
const fixture=JSON.parse(fs.readFileSync(process.argv[2],'utf8'));
let count=0;
const server=http.createServer(async(req,res)=>{for await(const chunk of req){};res.writeHead(200,{'Content-Type':'text/event-stream'});for(const event of (++count===1?fixture.first:fixture.second))res.write('data: '+JSON.stringify(event)+'\n\n');res.end();});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
try{const agent=new HttpAgent({url:'http://127.0.0.1:'+server.address().port+'/run',threadId:'thread',initialMessages:fixture.initial});await agent.runAgent({runId:'first'});await agent.runAgent({runId:'second'});const calls=agent.messages.flatMap(m=>m.role==='assistant'?m.toolCalls??[]:[]);if(calls.length!==4||new Set(calls.map(c=>c.id)).size!==4)throw Error('native aliases collided');const results=agent.messages.filter(m=>m.role==='tool');if(results.length!==4)throw Error('tool results lost');if(results.filter(m=>m.subagentRunId).length!==2)throw Error('child ownership lost');console.log('PASS four unique tool aliases across repeated native turn/sibling IDs');}finally{await new Promise(resolve=>server.close(resolve));}`
	command := exec.Command(node, "--input-type=module", "-e", script, module, path)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	t.Log(string(output))
}
