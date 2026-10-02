import { expect, it } from 'vitest';
import { conversationDTO, messageDTO, transcriptDTO } from './wireDTO';
it('normalizes retained Pascal DTOs while preserving arbitrary metadata and content keys', () => {
    const metadata = { KeepUpper: { ID: 'opaque' } };
    const result = conversationDTO({ Id: 'c', Shareable: 1, Metadata: metadata, Transcript: [{ Id: 't', Message: [{ Id: 'm', ConversationId: 'c', Content: '{"KeepUpper":1}' }] }] });
    expect(result.id).toBe('c'); expect(result.shareable).toBe(true);
    expect((result as any).Metadata).toBe(metadata);
    expect((result as any).transcript[0].message[0]).toMatchObject({ id: 'm', conversationId: 'c', content: '{"KeepUpper":1}' });
    expect(messageDTO({ id: 'preferred', Id: 'legacy' }).id).toBe('preferred');
    expect(transcriptDTO([{ Id: 't', Message: [] }]).turns[0].id).toBe('t');
});

it('omits nullable optional DTO aliases while retaining the wire fields', () => {
    const row = conversationDTO({ Id: 'c', AgentId: null, UsageInputTokens: null });
    expect(row.agentId).toBeUndefined(); expect(row.promptTokens).toBeUndefined();
    expect((row as unknown as Record<string, unknown>).AgentId).toBeNull();
    expect(messageDTO({ id: 'm', updatedAt: null }).updatedAt).toBeUndefined();
});
