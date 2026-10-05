import { afterEach, describe, expect, it, vi } from 'vitest';
import { AgentlyClient } from './client';
import { AgUiConversationTransport } from './aguiConversationTransport';
class Observer {
    static instances: Observer[] = [];
    onmessage?: (event: { data: string }) => void;
    onerror?: () => void;
    close = vi.fn();
    constructor(readonly url: string, readonly options: unknown) { Observer.instances.push(this); }
    emit(event: unknown) { this.onmessage?.({ data: JSON.stringify(event) }); }
}
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); Observer.instances = []; });
describe('explicit native application observation alongside AG-UI', () => {
    it('keeps primary execution separate and reconciles unknown provenance without rendering it', () => {
        vi.stubGlobal('EventSource', Observer);
        const primaryClose = vi.fn();
        vi.spyOn(AgUiConversationTransport.prototype, 'subscribe').mockReturnValue({ close: primaryClose });
        const refresh = vi.spyOn(AgUiConversationTransport.prototype, 'refresh').mockResolvedValue({} as never);
        const client = new AgentlyClient({ baseURL: '/v1', useCookies: true, observeNativeWork: true });
        const onEvent = vi.fn();
        const onTextDelta = vi.fn();
        const subscription = client.streamEvents('thread/one', { onEvent, onTextDelta });
        expect(Observer.instances).toHaveLength(1);
        const observer = Observer.instances[0];
        expect(observer.url).toBe('/v1/stream?conversationId=thread%2Fone&compatibilityScope=native-and-application');
        expect(observer.options).toEqual({ withCredentials: true });
        observer.emit({ type: 'text_delta', conversationId: 'thread/one', turnId: 'native-mobile', content: 'Mobile response' });
        expect(onTextDelta).toHaveBeenCalledWith('Mobile response', expect.objectContaining({ turnId: 'native-mobile' }));
        observer.emit({ type: 'compatibility_reconcile', conversationId: 'thread/one', turnId: 'unknown' });
        expect(refresh).toHaveBeenCalledWith('thread/one');
        expect(onEvent).toHaveBeenCalledTimes(1);
        observer.emit({ type: 'turn_completed', conversationId: 'thread/one', turnId: 'native-mobile' });
        expect(refresh).toHaveBeenCalledTimes(2);
        expect(onEvent).toHaveBeenCalledTimes(2);
        observer.emit({ type: 'conversation_meta_updated', conversationId: 'thread/one', patch: { aguiUpdated: true } });
        expect(refresh).toHaveBeenCalledTimes(3);
        subscription.close();
        expect(primaryClose).toHaveBeenCalledOnce();
        expect(observer.close).toHaveBeenCalledOnce();
    });
    it('does not require the application observer for primary AG-UI chat', () => {
        vi.stubGlobal('EventSource', Observer);
        vi.spyOn(AgUiConversationTransport.prototype, 'subscribe').mockReturnValue({ close: vi.fn() });
        const client = new AgentlyClient({ baseURL: '/v1', });
        client.streamEvents('thread', {});
        expect(Observer.instances).toHaveLength(0);
    });
    it('reconciles shared history without exposing the owner journal and fences account changes', async () => {
        vi.stubGlobal('EventSource', Observer);
        vi.spyOn(AgUiConversationTransport.prototype, 'subscribe').mockReturnValue({ close: vi.fn() });
        vi.spyOn(AgUiConversationTransport.prototype, 'refresh').mockRejectedValue({ status: 403 });
        const client = new AgentlyClient({ baseURL: '/v1', observeNativeWork: true });
        let release!: (value: never) => void;
        const transcript = vi.spyOn(client, 'readConversationHistory').mockImplementation(() => new Promise(resolve => { release = resolve; }));
        const onSnapshot = vi.fn();
        const onError = vi.fn();
        client.streamEvents('shared', { onSnapshot, onError });
        Observer.instances[0].emit({ type: 'compatibility_reconcile', conversationId: 'shared', turnId: 'native' });
        await vi.waitFor(() => expect(transcript).toHaveBeenCalled());
        client.resetAgUiInteractions();
        release({ conversation: { conversationId: 'shared', turns: [] } } as never);
        await Promise.resolve();
        expect(onSnapshot).not.toHaveBeenCalled();
        expect(onError).not.toHaveBeenCalled();
    });
    it('closes application observers at the account boundary and always requests application scope', () => {
        vi.stubGlobal('EventSource', Observer);
        const client = new AgentlyClient({ baseURL: '/v1', });
        client.observeNativeEvents('thread', {});
        client.resetAgUiInteractions();
        expect(Observer.instances[0].close).toHaveBeenCalledOnce();
        client.observeNativeEvents('application', {});
        expect(Observer.instances[1].url).toBe('/v1/stream?conversationId=application&compatibilityScope=native-and-application');
    });
});
