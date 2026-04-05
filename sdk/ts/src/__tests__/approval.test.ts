import { describe, expect, it, vi } from 'vitest';

import {
    buildApprovalEditorState,
    buildApprovalForgeSchema,
    executeApprovalCallbacks,
    normalizeApprovalMeta,
    serializeApprovalEditedFields,
} from '../approval';

describe('approval helpers', () => {
    it('normalizes approval metadata from raw JSON', () => {
        const meta = normalizeApprovalMeta(JSON.stringify({
            type: 'tool_approval',
            toolName: 'system/os/getEnv',
            title: 'OS Env Access',
            editors: [{
                name: 'names',
                kind: 'checkbox_list',
                path: 'names',
                options: [
                    { id: 'HOME', label: 'HOME', selected: true },
                    { id: 'SHELL', label: 'SHELL', selected: true },
                ],
            }],
        }));
        expect(meta?.type).toBe('tool_approval');
        expect(meta?.toolName).toBe('system/os/getEnv');
        expect(meta?.editors?.[0]?.options?.[0]?.id).toBe('HOME');
    });

    it('builds editor state and serializes edited fields', () => {
        const meta = normalizeApprovalMeta({
            type: 'tool_approval',
            editors: [{
                name: 'names',
                kind: 'checkbox_list',
                path: 'names',
                options: [
                    { id: 'HOME', label: 'HOME', selected: true },
                    { id: 'SHELL', label: 'SHELL', selected: true },
                    { id: 'PATH', label: 'PATH', selected: true },
                ],
            }],
        });
        expect(buildApprovalEditorState(meta)).toEqual({ names: ['HOME', 'SHELL', 'PATH'] });
        expect(serializeApprovalEditedFields(meta, { names: ['HOME', 'PATH'] })).toEqual({ names: ['HOME', 'PATH'] });
        expect(buildApprovalForgeSchema(meta)).toEqual({
            type: 'object',
            properties: {
                names: {
                    type: 'array',
                    title: 'names',
                    description: '',
                    enum: ['HOME', 'SHELL', 'PATH'],
                    default: ['HOME', 'SHELL', 'PATH'],
                    'x-ui-widget': 'multiSelect',
                },
            },
            required: [],
        });
    });

    it('executes approval callbacks through injected resolver', async () => {
        const handler = vi.fn().mockResolvedValue({
            editedFields: { names: ['HOME', 'PATH'] },
        });
        const payload = await executeApprovalCallbacks({
            meta: normalizeApprovalMeta({
                type: 'tool_approval',
                forge: {
                    callbacks: [{ event: 'approve', handler: 'approval.filterEnvNames' }],
                },
            }),
            event: 'approve',
            payload: {
                approval: { type: 'tool_approval' },
                editedFields: { names: ['HOME', 'SHELL', 'PATH'] },
                originalArgs: { names: ['HOME', 'SHELL', 'PATH'] },
            },
            resolveHandler(name) {
                if (name === 'approval.filterEnvNames') return handler;
                return null;
            },
        });

        expect(handler).toHaveBeenCalledTimes(1);
        expect(payload.editedFields).toEqual({ names: ['HOME', 'PATH'] });
    });

    it('applies callbacks in order and lets later callbacks win', async () => {
        const first = vi.fn().mockResolvedValue({
            editedFields: { names: ['HOME'] },
            action: 'approve',
        });
        const second = vi.fn().mockResolvedValue({
            editedFields: { names: ['PATH'] },
            action: 'reject',
        });

        const payload = await executeApprovalCallbacks({
            meta: normalizeApprovalMeta({
                type: 'tool_approval',
                forge: {
                    callbacks: [
                        { event: 'approve', handler: 'approval.first' },
                        { event: 'approve', handler: 'approval.second' },
                    ],
                },
            }),
            event: 'approve',
            payload: {
                approval: { type: 'tool_approval' },
                editedFields: { names: ['HOME', 'SHELL', 'PATH'] },
                originalArgs: { names: ['HOME', 'SHELL', 'PATH'] },
            },
            resolveHandler(name) {
                if (name === 'approval.first') return first;
                if (name === 'approval.second') return second;
                return null;
            },
        });

        expect(first).toHaveBeenCalledTimes(1);
        expect(second).toHaveBeenCalledTimes(1);
        expect(payload.editedFields).toEqual({ names: ['PATH'] });
        expect(payload.action).toBe('reject');
    });
});
