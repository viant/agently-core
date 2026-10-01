import type { Conversation, Message, Turn, TranscriptOutput } from './types';
type Row = Record<string, unknown>;
function fields(value: unknown, names: string[]): Row {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return value as Row;
    const row = { ...value } as Row;
    for (const name of names) {
        if (Object.prototype.hasOwnProperty.call(row, name)) {
            if (row[name] === null) delete row[name];
            continue;
        }
        const wire = name[0].toUpperCase() + name.slice(1);
        if (Object.prototype.hasOwnProperty.call(row, wire) && row[wire] !== null) row[name] = row[wire];
    }
    return row;
}
export function conversationDTO(value: unknown): Conversation {
    const row = fields(value, ['id','agentId','title','summary','stage','visibility','shareable','conversationParentId','createdAt','lastActivity','createdByUserId','promptTokens','completionTokens','totalTokens','cost']);
    if (!row) return row as unknown as Conversation;
    if (typeof row.shareable === 'number') row.shareable = row.shareable !== 0;
    if (row.promptTokens === undefined && typeof row.UsageInputTokens === 'number') row.promptTokens = row.UsageInputTokens;
    if (row.completionTokens === undefined && typeof row.UsageOutputTokens === 'number') row.completionTokens = row.UsageOutputTokens;
    if (Array.isArray(row.Transcript)) row.transcript = row.Transcript.map(turnDTO);
    return row as unknown as Conversation;
}
export function messageDTO(value: unknown): Message {
    return fields(value, ['id','conversationId','turnId','role','type','content','rawContent','status','interim','iteration','narration','phase','mode','sequence','archived','createdAt','updatedAt','createdByUserId','elicitationId','elicitationPayloadId','parentMessageId','linkedConversationId','attachmentPayloadId','toolName']) as unknown as Message;
}
export function turnDTO(value: unknown): Turn {
    const row = fields(value, ['id','turnId','conversationId','status','elapsedInSec','stage','queueSeq','agentIdUsed','modelOverride','modelOverrideProvider','startedByMessageId','errorMessage','runId','createdAt','message','execution','executionGroups']);
    if (row && Array.isArray(row.message)) row.message = row.message.map(messageDTO);
    return row as unknown as Turn;
}
export function transcriptDTO(value: unknown): TranscriptOutput {
    if (Array.isArray(value)) return { turns: value.map(turnDTO) };
    const row = fields(value, ['turns']);
    if (!row) return row as unknown as TranscriptOutput;
    if (Array.isArray(row.turns)) row.turns = row.turns.map(turnDTO);
    else {
        const conversation = fields(row.conversation, ['conversationId', 'turns']);
        if (conversation && Array.isArray(conversation.turns)) {
            row.turns = conversation.turns.map((value: unknown) => {
                const turn = turnDTO(value);
                return { ...turn, id: turn.id ?? turn.turnId, conversationId: turn.conversationId ?? conversation.conversationId };
            });
        }
    }
    return row as unknown as TranscriptOutput;
}
