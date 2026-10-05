// Deterministic protocol fixture only; this does not invoke Agently or a model.
import { createServer } from 'node:http';
import { randomUUID } from 'node:crypto';
const port = Number(process.env.MOCK_PORT || 8080);
createServer(async (req, res) => {
  if (req.method !== 'POST' || req.url !== '/v1/ag-ui/run') { res.writeHead(404).end(); return; }
  try {
    let body = ''; for await (const chunk of req) body += chunk;
    const input = JSON.parse(body);
    res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' });
    const emit = event => res.write(`data: ${JSON.stringify(event)}\n\n`);
    emit({ type: 'RUN_STARTED', threadId: input.threadId, runId: input.runId });
    if (input.forwardedProps?.agently?.operation === 'capabilities') {
      emit({ type: 'CUSTOM', name: 'agently.capabilities', value: { version: '1', capabilities: { custom: { agently: { version: '1', operations: ['chat', 'capabilities'], fixture: true } } } } });
    } else {
      const messageId = randomUUID();
      emit({ type: 'TEXT_MESSAGE_START', messageId, role: 'assistant' });
      const payload = input.forwardedProps?.agently?.payload || {};
      const text = `Fixture response streamed through AG-UI 1.0.1. Agent: ${payload.agentId || 'default'}; model: ${payload.model || 'default'}.`;
      for (const delta of text.match(/.{1,14}/g) || []) {
        emit({ type: 'TEXT_MESSAGE_CONTENT', messageId, delta });
        await new Promise(resolve => setTimeout(resolve, 30));
        if (res.destroyed) return;
      }
      emit({ type: 'TEXT_MESSAGE_END', messageId });
    }
    emit({ type: 'RUN_FINISHED', threadId: input.threadId, runId: input.runId });
    res.end();
  } catch { if (!res.headersSent) res.writeHead(400); res.end(); }
}).listen(port, '127.0.0.1', () => console.log(`AG-UI fixture listening on http://127.0.0.1:${port}`));
