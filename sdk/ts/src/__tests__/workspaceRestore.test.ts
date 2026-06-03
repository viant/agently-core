import { describe, expect, it } from 'vitest';

import { deriveHostedWorkspaceRestoreStateFromTranscriptTurns } from '../workspaceRestore';

describe('deriveHostedWorkspaceRestoreStateFromTranscriptTurns', () => {
    it('restores compare windows from the last turn ui/window/list and ui/window/show steps', () => {
        expect(deriveHostedWorkspaceRestoreStateFromTranscriptTurns([
            {
                turnId: 'turn-1',
                execution: {
                    pages: [
                        {
                            toolSteps: [
                                {
                                    toolName: 'ui/window/list',
                                    status: 'completed',
                                    responsePayload: {
                                        focusedWindowId: 'order_2609393',
                                        items: [
                                            {
                                                windowId: 'chat/new',
                                                windowKey: 'chat/new',
                                                windowTitle: 'Chat',
                                                inTab: true,
                                            },
                                            {
                                                windowId: 'order_2656980',
                                                conversationId: 'conv-1',
                                                windowKey: 'order',
                                                windowTitle: 'Order Summary',
                                                presentation: 'hosted',
                                                region: 'chat.top',
                                                parentKey: 'chat/new',
                                                inTab: true,
                                                parameters: { AdOrderId: [2656980] },
                                            },
                                            {
                                                windowId: 'order_2609393',
                                                conversationId: 'conv-1',
                                                windowKey: 'order',
                                                windowTitle: 'Order Summary',
                                                presentation: 'hosted',
                                                region: 'chat.top',
                                                parentKey: 'chat/new',
                                                inTab: true,
                                                parameters: { AdOrderId: [2609393] },
                                            },
                                        ],
                                    },
                                },
                                {
                                    toolName: 'ui/window/show',
                                    status: 'completed',
                                    requestPayload: {
                                        windowId: 'order_2656980',
                                    },
                                    responsePayload: { ok: true },
                                },
                            ],
                        },
                    ],
                },
            } as any,
        ])).toEqual({
            windows: [
                {
                    windowId: 'order_2656980',
                    conversationId: 'conv-1',
                    windowKey: 'order',
                    windowTitle: 'Order Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: { AdOrderId: [2656980] },
                },
                {
                    windowId: 'order_2609393',
                    conversationId: 'conv-1',
                    windowKey: 'order',
                    windowTitle: 'Order Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: { AdOrderId: [2609393] },
                },
            ],
            selectedWindowId: 'order_2656980',
        });
    });

    it('restores hosted workspace state from the last turn only', () => {
        expect(deriveHostedWorkspaceRestoreStateFromTranscriptTurns([
            {
                turnId: 'turn-1',
                execution: {
                    pages: [
                        {
                            toolSteps: [
                                {
                                    toolName: 'ui/window/list',
                                    status: 'completed',
                                    responsePayload: {
                                        items: [
                                            {
                                                windowId: 'order_legacy',
                                                conversationId: 'conv-1',
                                                windowKey: 'order',
                                                windowTitle: 'Order Summary',
                                                presentation: 'hosted',
                                                region: 'chat.top',
                                                parentKey: 'chat/new',
                                                inTab: true,
                                                parameters: { AdOrderId: [111] },
                                            },
                                        ],
                                    },
                                },
                            ],
                        },
                    ],
                },
            } as any,
            {
                turnId: 'turn-2',
                execution: {
                    pages: [
                        {
                            toolSteps: [
                                {
                                    toolName: 'ui/window/list',
                                    status: 'completed',
                                    responsePayload: { items: [] },
                                },
                            ],
                        },
                    ],
                },
            } as any,
        ])).toBeNull();
    });

    it('does not backfill older hosted view-open state when the last turn has no hosted workspace', () => {
        expect(deriveHostedWorkspaceRestoreStateFromTranscriptTurns([
            {
                turnId: 'turn-1',
                execution: {
                    pages: [
                        {
                            toolSteps: [
                                {
                                    toolName: 'ui/view/open',
                                    status: 'completed',
                                    requestPayload: {
                                        id: 'metricReportBuilder',
                                        parameters: {
                                            metrics_ad_cube_report: {
                                                parameters: {
                                                    filters: { channelIds: [1] },
                                                },
                                            },
                                        },
                                    },
                                    responsePayload: {
                                        windowId: 'metricReportBuilder__conv-1',
                                        windowKey: 'metricReportBuilder',
                                        windowTitle: 'Performance Metrics',
                                        conversationId: 'conv-1',
                                        presentation: 'hosted',
                                        region: 'chat.top',
                                        parentKey: 'chat/new',
                                        selectedWindowId: 'metricReportBuilder__conv-1',
                                    },
                                },
                            ],
                        },
                    ],
                },
            } as any,
            {
                turnId: 'turn-2',
                execution: {
                    pages: [
                        {
                            toolSteps: [
                                {
                                    toolName: 'message/reply',
                                    status: 'completed',
                                    responsePayload: { ok: true },
                                },
                            ],
                        },
                    ],
                },
            } as any,
        ])).toBeNull();
    });

    it('restores hosted ui/view/open state from tool content when the response payload is a gzip envelope', () => {
        expect(deriveHostedWorkspaceRestoreStateFromTranscriptTurns([
            {
                turnId: 'turn-1',
                execution: {
                    pages: [
                        {
                            toolSteps: [
                                {
                                    toolName: 'ui/view/open',
                                    status: 'completed',
                                    requestPayload: {
                                        InlineBody: JSON.stringify({
                                            id: 'order',
                                            parameters: {
                                                AdOrderId: [2673453],
                                            },
                                        }),
                                        Compression: 'none',
                                    },
                                    responsePayload: {
                                        InlineBody: '\u0001\u0002garbled',
                                        Compression: 'gzip',
                                    },
                                    content: JSON.stringify({
                                        conversationId: 'conv-1',
                                        items: [
                                            {
                                                conversationId: 'conv-1',
                                                parameters: {
                                                    AdOrderId: [2673453],
                                                },
                                                parentKey: 'chat/new',
                                                presentation: 'hosted',
                                                region: 'chat.top',
                                                windowId: 'order_2345888602__conv-1',
                                                windowKey: 'order',
                                                windowTitle: 'Order Summary',
                                            },
                                        ],
                                        ok: true,
                                        parentKey: 'chat/new',
                                        presentation: 'hosted',
                                        region: 'chat.top',
                                        selectedWindowId: 'order_2345888602__conv-1',
                                        windowId: 'order_2345888602__conv-1',
                                        windowKey: 'order',
                                        windowTitle: 'Order Summary',
                                    }),
                                },
                            ],
                        },
                    ],
                },
            } as any,
        ])).toEqual({
            windows: [
                {
                    windowId: 'order_2345888602__conv-1',
                    conversationId: 'conv-1',
                    windowKey: 'order',
                    windowTitle: 'Order Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: {
                        AdOrderId: [2673453],
                    },
                },
            ],
            selectedWindowId: 'order_2345888602__conv-1',
        });
    });

    it('uses tool content JSON when the payload wrapper is only a transport envelope', () => {
        expect(deriveHostedWorkspaceRestoreStateFromTranscriptTurns([
            {
                turnId: 'turn-1',
                execution: {
                    pages: [
                        {
                            toolSteps: [
                                {
                                    toolName: 'ui/window/list',
                                    status: 'completed',
                                    responsePayload: {
                                        inlineBody: '\u0001\u0002garbled',
                                        compression: 'gzip',
                                    },
                                    content: JSON.stringify({
                                        focusedWindowId: 'order_2609393__conv-1',
                                        items: [
                                            {
                                                windowId: 'order_2656980__conv-1',
                                                conversationId: 'conv-1',
                                                windowKey: 'order',
                                                windowTitle: 'Order Summary',
                                                presentation: 'hosted',
                                                region: 'chat.top',
                                                parentKey: 'chat/new',
                                                inTab: true,
                                                parameters: { AdOrderId: [2656980] },
                                            },
                                            {
                                                windowId: 'order_2609393__conv-1',
                                                conversationId: 'conv-1',
                                                windowKey: 'order',
                                                windowTitle: 'Order Summary',
                                                presentation: 'hosted',
                                                region: 'chat.top',
                                                parentKey: 'chat/new',
                                                inTab: true,
                                                parameters: { AdOrderId: [2609393] },
                                            },
                                        ],
                                    }),
                                },
                            ],
                        },
                    ],
                },
            } as any,
        ])).toEqual({
            windows: [
                {
                    windowId: 'order_2656980__conv-1',
                    conversationId: 'conv-1',
                    windowKey: 'order',
                    windowTitle: 'Order Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: { AdOrderId: [2656980] },
                },
                {
                    windowId: 'order_2609393__conv-1',
                    conversationId: 'conv-1',
                    windowKey: 'order',
                    windowTitle: 'Order Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: { AdOrderId: [2609393] },
                },
            ],
            selectedWindowId: 'order_2609393__conv-1',
        });
    });

    it('uses the final ui/window/show request payload when the transcript stores InlineBody envelopes', () => {
        expect(deriveHostedWorkspaceRestoreStateFromTranscriptTurns([
            {
                turnId: 'turn-1',
                execution: {
                    pages: [
                        {
                            toolSteps: [
                                {
                                    toolName: 'ui/window/list',
                                    status: 'completed',
                                    responsePayload: {
                                        InlineBody: JSON.stringify({
                                            focusedWindowId: 'order_2609393__conv-1',
                                            items: [
                                                {
                                                    windowId: 'order_2656980__conv-1',
                                                    conversationId: 'conv-1',
                                                    windowKey: 'order',
                                                    windowTitle: 'Order Summary',
                                                    presentation: 'hosted',
                                                    region: 'chat.top',
                                                    parentKey: 'chat/new',
                                                    inTab: true,
                                                    parameters: { AdOrderId: [2656980] },
                                                },
                                                {
                                                    windowId: 'order_2609393__conv-1',
                                                    conversationId: 'conv-1',
                                                    windowKey: 'order',
                                                    windowTitle: 'Order Summary',
                                                    presentation: 'hosted',
                                                    region: 'chat.top',
                                                    parentKey: 'chat/new',
                                                    inTab: true,
                                                    parameters: { AdOrderId: [2609393] },
                                                },
                                            ],
                                        }),
                                        Compression: 'none',
                                    },
                                },
                                {
                                    toolName: 'ui/window/show',
                                    status: 'completed',
                                    requestPayload: {
                                        InlineBody: JSON.stringify({
                                            windowId: 'order_2656980__conv-1',
                                        }),
                                        Compression: 'none',
                                    },
                                    responsePayload: { ok: true },
                                },
                            ],
                        },
                    ],
                },
            } as any,
        ])).toEqual({
            windows: [
                {
                    windowId: 'order_2656980__conv-1',
                    conversationId: 'conv-1',
                    windowKey: 'order',
                    windowTitle: 'Order Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: { AdOrderId: [2656980] },
                },
                {
                    windowId: 'order_2609393__conv-1',
                    conversationId: 'conv-1',
                    windowKey: 'order',
                    windowTitle: 'Order Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: { AdOrderId: [2609393] },
                },
            ],
            selectedWindowId: 'order_2656980__conv-1',
        });
    });
});
