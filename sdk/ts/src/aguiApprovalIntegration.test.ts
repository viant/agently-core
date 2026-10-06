import { afterEach, describe, expect, it, vi } from 'vitest';
import { AgentlyClient } from './client';
import { AgUiConversationTransport } from './aguiConversationTransport';
const protocol = { version: '1', threadId: 'thread', originalRunId: 'original', commandRunId: 'decision', remainingInterruptIds: ['other'] };
afterEach(() => vi.restoreAllMocks());
describe('approval outcomes discover genuine AG-UI continuation', () => {
    it('reconciles the native conversation for an MCP proxy without treating its synthetic thread as chat', async () => {
        const refresh = vi.spyOn(AgUiConversationTransport.prototype, 'refresh').mockResolvedValue({} as never);
        const references = { ...protocol, kind: 'mcp-app', threadId: 'synthetic', nativeConversationId: 'native' };
        const client = new AgentlyClient({ baseURL: '/v1', fetchImpl: async () => new Response(JSON.stringify({ status: 'ok', protocol: references })) });
        await client.decideToolApproval('one', { action: 'approve' });
        expect(refresh).toHaveBeenCalledWith('native');
        expect(refresh).not.toHaveBeenCalledWith('synthetic');
    });
    it('returns the real decision outcome and discovers current runs even when replayed refs have no successor', async () => {
        const refresh = vi.spyOn(AgUiConversationTransport.prototype, 'refresh').mockResolvedValue({} as never);
        const output = { status: 'ok', protocol, outcome: { approvalId: 'one', action: 'approve', result: 'actual result' } };
        const fetchImpl = vi.fn(async () => new Response(JSON.stringify(output)));
        const client = new AgentlyClient({ baseURL: '/v1', fetchImpl });
        expect(await client.decideToolApproval('one', { action: 'approve' })).toEqual(output);
        expect(fetchImpl.mock.calls).toHaveLength(1);
        expect(refresh).toHaveBeenCalledWith('thread');
    });
    it('does not turn successful decisions into failures when observation is unavailable', async () => {
        vi.spyOn(AgUiConversationTransport.prototype, 'refresh').mockRejectedValue(new Error('reconnect needed'));
        const onError = vi.fn();
        const client = new AgentlyClient({ baseURL: '/v1', onError,
            fetchImpl: async () => new Response(JSON.stringify({ status: 'ok', protocol })) });
        await expect(client.decideToolApproval('one', { action: 'approve' })).resolves.toMatchObject({ status: 'ok' });
        await vi.waitFor(() => expect(onError).toHaveBeenCalled());
    });
    it('reconciles another client outcome once per protocol receipt while preserving the inbox payload', async () => {
        const refresh = vi.spyOn(AgUiConversationTransport.prototype, 'refresh').mockResolvedValue({} as never);
        const outcome = { approvalId: 'one', action: 'approve', result: 'actual result', protocol };
        const client = new AgentlyClient({ baseURL: '/v1', fetchImpl: async () => new Response(JSON.stringify({ rows: [], outcomes: [outcome] })) });
        expect((await client.listPendingToolApprovalsPage()).outcomes).toEqual([outcome]);
        await client.listPendingToolApprovalsPage();
        expect(refresh).toHaveBeenCalledTimes(1);
        client.resetAgUiInteractions();
        await client.listPendingToolApprovalsPage();
        expect(refresh).toHaveBeenCalledTimes(2);
    });
    it('does not invent protocol reconciliation for an application decision without run references', async () => {
        const refresh = vi.spyOn(AgUiConversationTransport.prototype, 'refresh');
        const client = new AgentlyClient({ baseURL: '/v1', fetchImpl: async () => new Response(JSON.stringify({ status: 'ok' })) });
        await client.decideToolApproval('one', { action: 'approve' });
        expect(refresh).not.toHaveBeenCalled();
    });
    it('does not attach the previous account continuation after logout during a decision request', async () => {
        const refresh = vi.spyOn(AgUiConversationTransport.prototype, 'refresh').mockResolvedValue({} as never);
        let respond!: (response: Response) => void;
        const started = vi.fn();
        const client = new AgentlyClient({ baseURL: '/v1', fetchImpl: async () => {
            started(); return new Promise<Response>(resolve => { respond = resolve; });
        } });
        const decision = client.decideToolApproval('one', { action: 'approve' });
        await vi.waitFor(() => expect(started).toHaveBeenCalled());
        client.resetAgUiInteractions();
        respond(new Response(JSON.stringify({ status: 'ok', protocol })));
        await decision;
        expect(refresh).not.toHaveBeenCalled();
    });
});
