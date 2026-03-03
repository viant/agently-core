/**
 * interpret.ts — Helpers for interpreting message roles, phases, and iteration structure.
 *
 * Used by the UI's message normalization layer to classify messages for rendering
 * (preamble bubbles, tool call rows, final response bubbles, etc.)
 */

import type { Message, ToolMessageView } from './types';

// ─── Message classification ────────────────────────────────────────────────────

/** Is this message a preamble (assistant thinking/reasoning before tool calls)? */
export function isPreamble(msg: Message): boolean {
    return msg.role === 'assistant' && msg.interim === 1;
}

/** Is this message a final assistant response (not interim)? */
export function isFinalResponse(msg: Message): boolean {
    return msg.role === 'assistant' && msg.interim === 0;
}

/** Is this a user message? */
export function isUserMessage(msg: Message): boolean {
    return msg.role === 'user';
}

/** Is this a tool result message? */
export function isToolMessage(msg: Message): boolean {
    return msg.role === 'tool' || (msg.toolMessage != null && msg.toolMessage.length > 0);
}

/** Is this a system/control message? */
export function isSystemMessage(msg: Message): boolean {
    return msg.role === 'system' || msg.type === 'control';
}

/** Is this message archived (should not be displayed)? */
export function isArchived(msg: Message): boolean {
    return (msg.archived ?? 0) !== 0;
}

/** Is this a summary message? */
export function isSummary(msg: Message): boolean {
    return msg.status === 'summary';
}

/** Is this message summarized (replaced by a summary, should be hidden)? */
export function isSummarized(msg: Message): boolean {
    return msg.status === 'summarized';
}

// ─── Tool call extraction ──────────────────────────────────────────────────────

/** Extract the primary tool name from a message's toolMessage array. */
export function toolName(msg: Message): string | null {
    const tm = firstToolMessage(msg);
    return tm?.toolCall?.toolName ?? tm?.toolName ?? msg.toolName ?? null;
}

/** Get the status of the first tool call in the message. */
export function toolStatus(msg: Message): string | null {
    const tm = firstToolMessage(msg);
    return tm?.toolCall?.status ?? null;
}

/** Get elapsed time in ms for the first tool call. */
export function toolElapsedMs(msg: Message): number | null {
    const tm = firstToolMessage(msg);
    return tm?.toolCall?.elapsedMs ?? null;
}

/** Get the tool call trace ID for correlation. */
export function toolCallId(msg: Message): string | null {
    const tm = firstToolMessage(msg);
    return tm?.toolCall?.traceId ?? tm?.toolCall?.id ?? null;
}

/** Get the iteration number for a message. */
export function messageIteration(msg: Message): number | null {
    return msg.iteration ?? null;
}

/** Get the preamble text for a message (if set explicitly). */
export function messagePreamble(msg: Message): string | null {
    return msg.preamble ?? null;
}

// ─── Iteration grouping helpers ────────────────────────────────────────────────

/**
 * Groups messages within a single turn by their `iteration` field.
 *
 * Returns a map: iteration number → messages in that iteration.
 * Messages without an iteration number are grouped under key -1.
 */
export function groupByIteration(messages: Message[]): Map<number, Message[]> {
    const groups = new Map<number, Message[]>();
    for (const msg of messages) {
        const key = msg.iteration ?? -1;
        const group = groups.get(key) || [];
        group.push(msg);
        groups.set(key, group);
    }
    return groups;
}

/**
 * Determines the message type for UI rendering classification.
 *
 * Returns one of: 'user', 'preamble', 'tool', 'response', 'summary',
 * 'summarized', 'system', 'elicitation', 'unknown'.
 */
export function messageUIType(msg: Message): string {
    if (isSummarized(msg)) return 'summarized';
    if (isSummary(msg)) return 'summary';
    if (isArchived(msg)) return 'archived';
    if (isUserMessage(msg)) return 'user';
    if (isSystemMessage(msg)) return 'system';
    if (msg.elicitationId && msg.status !== 'accepted') return 'elicitation';
    if (isToolMessage(msg)) return 'tool';
    if (isPreamble(msg)) return 'preamble';
    if (isFinalResponse(msg)) return 'response';
    return 'unknown';
}

// ─── Internal ──────────────────────────────────────────────────────────────────

function firstToolMessage(msg: Message): ToolMessageView | null {
    if (msg.toolMessage?.length) return msg.toolMessage[0];
    return null;
}
