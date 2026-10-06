import { useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { HttpAgent } from '@ag-ui/client';
import { CopilotKit, CopilotChat, useDefaultRenderTool } from '@copilotkit/react-core/v2';
import '@copilotkit/react-core/v2/styles.css';
import './style.css';

function Chat() {
  useDefaultRenderTool();
  return <CopilotChat agentId="agently" />;
}

function App() {
  const [agentId, setAgentId] = useState('');
  const [model, setModel] = useState('');
  const [capabilities, setCapabilities] = useState<unknown>();
  const [status, setStatus] = useState('Capabilities have not been requested.');
  const [discovering, setDiscovering] = useState(false);
  const config = useRef({ agentId, model });
  config.current = { agentId, model };
  const agent = useMemo(() => new HttpAgent({
    url: '/v1/ag-ui/run',
    fetch: (url, init) => {
      const input = JSON.parse(String(init.body));
      const payload = Object.fromEntries(Object.entries(config.current).filter(([, value]) => value.trim()));
      if (!input.forwardedProps?.__proxiedMCPRequest) {
        input.forwardedProps = { ...input.forwardedProps, agently: { version: '1', operation: 'chat', payload } };
      } else {
        // The upstream renderer clones the chat agent for an isolated host
        // request. Forward only that request, without inherited model inputs.
        input.messages = [];
        input.tools = [];
        input.state = {};
        input.context = [];
        if (!input.resume?.length) input.threadId = input.runId;
      }
      return fetch(url, { ...init, credentials: 'same-origin', body: JSON.stringify(input) });
    },
  }), []);
  async function discover() {
    setDiscovering(true);
    setStatus('Requesting capabilities…');
    const probe = new HttpAgent({ url: '/v1/ag-ui/run', fetch: (url, init) => fetch(url, { ...init, credentials: 'same-origin' }) });
    let received = false;
    try {
      await probe.runAgent({ forwardedProps: { agently: { version: '1', operation: 'capabilities' } } }, {
        onRunErrorEvent: ({ event }) => { throw new Error(event.message); },
        onCustomEvent: ({ event }) => { if (event.name === 'agently.capabilities') { received = true; setCapabilities(event.value); } },
      });
      setStatus(received ? 'Capabilities received.' : 'Run completed without an Agently capability event.');
    } catch (error) { setStatus(`Capability request failed: ${error instanceof Error ? error.message : String(error)}`); }
    finally { setDiscovering(false); }
  }
  return <main>
    <aside aria-label="Agently extension configuration">
      <h1>Agently AG-UI shell</h1>
      <p>CopilotKit 1.77.0 · AG-UI 1.0.1</p>
      <label>Agent ID<input value={agentId} onChange={e => setAgentId(e.target.value)} placeholder="Backend default" /></label>
      <label>Model<input value={model} onChange={e => setModel(e.target.value)} placeholder="Backend default" /></label>
      <p className="hint">Selections apply to the next chat run through the Agently v1 extension.</p>
      <button onClick={discover} disabled={discovering}>{discovering ? 'Requesting…' : 'Discover capabilities'}</button>
      <p role="status">{status}</p>
      {capabilities !== undefined && <details open><summary>Agently capabilities</summary><pre>{JSON.stringify(capabilities, null, 2)}</pre></details>}
      <p className="hint">Local development UI. Chat and streaming are provided by the upstream CopilotKit component.</p>
    </aside>
    <section className="chat" aria-label="Agent chat"><CopilotKit enableInspector={false} agents__unsafe_dev_only={{ agently: agent }}><Chat /></CopilotKit></section>
  </main>;
}
createRoot(document.getElementById('root')!).render(<App />);
