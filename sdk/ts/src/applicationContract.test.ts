import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { AgentlyClient } from './client';
import { HttpError } from './errors';

// This suite runs only against e2e/sdkcontract/cmd/server, the actual
// disposable application runtime. Tokens remain in private temporary files.
const readyPath = process.env.AGENTLY_SDK_CONTRACT_READY;
const ready = readyPath ? JSON.parse(readFileSync(readyPath, 'utf8')) : undefined;
const suite = ready ? describe : describe.skip;
suite('actual SDK1 application HTTP contract', () => {
    const client = (tokenFile?: string, unauthorized?: (e: HttpError) => void) => new AgentlyClient({
        baseURL: `${ready.url.replace(/\/$/, '')}/v1`, retries: 0,
        tokenProvider: tokenFile ? () => readFileSync(tokenFile, 'utf8').trim() : undefined,
        onUnauthorized: unauthorized,
    });
    it('reads native conversation/message JSON and preserves protected mutation authorization', async () => {
        const owner = client(ready.ownerTokenFile);
        const conversation = await owner.getConversation(ready.conversationId);
        expect(conversation.id).toBe(ready.conversationId);
        const page = await owner.listConversations({ page: { limit: 1 } });
        expect(page.data).toHaveLength(1);
        expect(page.page?.hasMore).toBe(true);
        const messages = await owner.getMessages({ conversationId: ready.conversationId });
        expect(messages.data.length).toBeGreaterThan(0);
        expect(messages.data.every(row => row.conversationId === ready.conversationId)).toBe(true);
        const transcript = await owner.getTranscript({ conversationId: ready.conversationId });
        expect(transcript.turns[0].id).toBe('sdk-contract-turn');
        expect((transcript as any).conversation.turns[0].user.content).toBe('fixture prompt');
        await expect(client(ready.otherTokenFile).deleteConversation(ready.conversationId)).rejects.toMatchObject({ status: 403 });
        expect((await owner.getConversation(ready.conversationId)).id).toBe(ready.conversationId);
    });
    it('delivers authenticated usage SSE through the public client', async () => {
        const owner = client(ready.ownerTokenFile);
        await new Promise<void>((resolve, reject) => {
            const timeout = setTimeout(() => { subscription.close(); reject(new Error('usage SSE timed out')); }, 5000);
            const subscription = owner.streamEvents(ready.conversationId, {
                onEvent: event => {
                    if (event.type !== 'usage') return;
                    try {
                        expect(event.conversationId).toBe(ready.conversationId);
                        expect(event.patch).toMatchObject({ inputTokens: 11, outputTokens: 7 });
                        subscription.close(); clearTimeout(timeout); resolve();
                    } catch (error) { subscription.close(); clearTimeout(timeout); reject(error); }
                },
                onError: error => { subscription.close(); clearTimeout(timeout); reject(new Error(error)); },
            });
        });
    });
    it('reports rejected bearer SSE as unauthorized', async () => {
        const statuses: number[] = [];
        const invalid = new AgentlyClient({ baseURL: `${ready.url}/v1`, tokenProvider: () => 'invalid-fixture-token', onUnauthorized: error => statuses.push(error.status) });
        await new Promise<void>((resolve, reject) => {
            const timeout = setTimeout(() => { subscription.close(); reject(new Error('unauthorized SSE timed out')); }, 5000);
            const subscription = invalid.streamEvents(ready.conversationId, { onError: error => {
                try { expect(error).toBe('SSE unauthorized (401)'); expect(statuses).toEqual([401]); subscription.close(); clearTimeout(timeout); resolve(); }
                catch (failure) { subscription.close(); clearTimeout(timeout); reject(failure); }
            } });
        });
    });
    it('preserves unauthorized HTTP status, response body, and callback', async () => {
        const seen: HttpError[] = [];
        try {
            await client(undefined, e => seen.push(e)).getConversation(ready.conversationId);
            throw new Error('anonymous request unexpectedly succeeded');
        } catch (error) {
            expect(error).toBeInstanceOf(HttpError);
            expect((error as HttpError).status).toBe(401);
            expect((error as HttpError).body.length).toBeGreaterThan(0);
            expect(seen).toHaveLength(1);
            expect(seen[0]).toBe(error);
        }
    });
});
