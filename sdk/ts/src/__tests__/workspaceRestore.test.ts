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
                                        focusedWindowId: 'report_2609393',
                                        items: [
                                            {
                                                windowId: 'chat/new',
                                                windowKey: 'chat/new',
                                                windowTitle: 'Chat',
                                                inTab: true,
                                            },
                                            {
                                                windowId: 'report_2656980',
                                                conversationId: 'conv-1',
                                                windowKey: 'report',
                                                windowTitle: 'Report Summary',
                                                presentation: 'hosted',
                                                region: 'chat.top',
                                                parentKey: 'chat/new',
                                                inTab: true,
                                                parameters: { entityId: [2656980] },
                                            },
                                            {
                                                windowId: 'report_2609393',
                                                conversationId: 'conv-1',
                                                windowKey: 'report',
                                                windowTitle: 'Report Summary',
                                                presentation: 'hosted',
                                                region: 'chat.top',
                                                parentKey: 'chat/new',
                                                inTab: true,
                                                parameters: { entityId: [2609393] },
                                            },
                                        ],
                                    },
                                },
                                {
                                    toolName: 'ui/window/show',
                                    status: 'completed',
                                    requestPayload: {
                                        windowId: 'report_2656980',
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
                    windowId: 'chat/new',
                    conversationId: null,
                    windowKey: 'chat/new',
                    windowTitle: 'Chat',
                    presentation: null,
                    region: null,
                    parentKey: '',
                    inTab: true,
                    parameters: {},
                },
                {
                    windowId: 'report_2656980',
                    conversationId: 'conv-1',
                    windowKey: 'report',
                    windowTitle: 'Report Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: { entityId: [2656980] },
                },
                {
                    windowId: 'report_2609393',
                    conversationId: 'conv-1',
                    windowKey: 'report',
                    windowTitle: 'Report Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: { entityId: [2609393] },
                },
            ],
            selectedWindowId: 'report_2656980',
        });
    });

    it('restores generic windows without Agently placement fields', () => {
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
                                        id: 'report-builder',
                                        parameters: { reportId: 'summary' },
                                    },
                                    responsePayload: {
                                        windowId: 'report-summary',
                                        windowKey: 'report-builder',
                                        windowTitle: 'Summary',
                                        selectedWindowId: 'report-summary',
                                    },
                                },
                            ],
                        },
                    ],
                },
            } as any,
        ])).toEqual({
            windows: [
                {
                    windowId: 'report-summary',
                    conversationId: null,
                    windowKey: 'report-builder',
                    windowTitle: 'Summary',
                    presentation: null,
                    region: null,
                    parentKey: '',
                    inTab: true,
                    parameters: { reportId: 'summary' },
                },
            ],
            selectedWindowId: 'report-summary',
        });
    });

    it('restores hosted workspace state from ui/window/open and later form data', () => {
        expect(deriveHostedWorkspaceRestoreStateFromTranscriptTurns([
            {
                turnId: 'turn-1',
                execution: {
                    pages: [
                        {
                            toolSteps: [
                                {
                                    toolName: 'ui/window/open',
                                    status: 'completed',
                                    requestPayload: {
                                        windowKey: 'reportBuilder',
                                        parameters: {
                                            reportBuilderRef: 'capacityBuilder',
                                        },
                                    },
                                    responsePayload: {
                                        windowId: 'reportBuilder__conv-1',
                                        conversationId: 'conv-1',
                                        windowKey: 'reportBuilder',
                                        windowTitle: 'Capacity Builder',
                                        presentation: 'hosted',
                                        region: 'chat.top',
                                        parentKey: 'chat/new',
                                        workspaceMinHeight: 500,
                                        workspaceSharePct: 72,
                                    },
                                },
                                {
                                    toolName: 'ui/window:setFormData',
                                    status: 'completed',
                                    requestPayload: {
                                        windowId: 'reportBuilder__conv-1',
                                        values: {
                                            prefill: {
                                                scope: {
                                                    targetKey: 'record:12345',
                                                },
                                            },
                                        },
                                    },
                                    responsePayload: {
                                        windowId: 'reportBuilder__conv-1',
                                        windowForm: {
                                            reportBuilderRef: 'capacityBuilder',
                                            prefill: {
                                                scope: {
                                                    targetKey: 'record:12345',
                                                },
                                            },
                                        },
                                    },
                                },
                            ],
                        },
                    ],
                },
            } as any,
        ])).toEqual({
            windows: [
                {
                    windowId: 'reportBuilder__conv-1',
                    conversationId: 'conv-1',
                    windowKey: 'reportBuilder',
                    windowTitle: 'Capacity Builder',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    workspaceSharePct: 72,
                    workspaceMinHeight: 500,
                    inTab: true,
                    parameters: {
                        reportBuilderRef: 'capacityBuilder',
                    },
                    windowForm: {
                        reportBuilderRef: 'capacityBuilder',
                        prefill: {
                            scope: {
                                targetKey: 'record:12345',
                            },
                        },
                    },
                },
            ],
            selectedWindowId: 'reportBuilder__conv-1',
        });
    });

    it('preserves hosted workspace state when a later turn lists no live windows', () => {
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
                                                windowId: 'report_legacy',
                                                conversationId: 'conv-1',
                                                windowKey: 'report',
                                                windowTitle: 'Report Summary',
                                                presentation: 'hosted',
                                                region: 'chat.top',
                                                parentKey: 'chat/new',
                                                inTab: true,
                                                parameters: { entityId: [111] },
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
        ])).toMatchObject({
            selectedWindowId: 'report_legacy',
            windows: [{ windowId: 'report_legacy', parameters: { entityId: [111] } }],
        });
    });

    it('restores older hosted view-open state when later turns do not close it', () => {
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
        ])).toMatchObject({
            selectedWindowId: 'metricReportBuilder__conv-1',
            windows: [{
                windowId: 'metricReportBuilder__conv-1',
                parameters: {
                    metrics_ad_cube_report: {
                        parameters: { filters: { channelIds: [1] } },
                    },
                },
            }],
        });
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
                                            id: 'report',
                                            parameters: {
                                                entityId: [2673453],
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
                                                    entityId: [2673453],
                                                },
                                                parentKey: 'chat/new',
                                                presentation: 'hosted',
                                                region: 'chat.top',
                                                windowId: 'report_2345888602__conv-1',
                                                windowKey: 'report',
                                                windowTitle: 'Report Summary',
                                                workspaceSharePct: 72,
                                                workspaceMinHeight: 500,
                                            },
                                        ],
                                        ok: true,
                                        parentKey: 'chat/new',
                                        presentation: 'hosted',
                                        region: 'chat.top',
                                        selectedWindowId: 'report_2345888602__conv-1',
                                        windowId: 'report_2345888602__conv-1',
                                        windowKey: 'report',
                                        windowTitle: 'Report Summary',
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
                    windowId: 'report_2345888602__conv-1',
                    conversationId: 'conv-1',
                    windowKey: 'report',
                    windowTitle: 'Report Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    workspaceSharePct: 72,
                    workspaceMinHeight: 500,
                    inTab: true,
                    parameters: {
                        entityId: [2673453],
                    },
                },
            ],
            selectedWindowId: 'report_2345888602__conv-1',
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
                                        focusedWindowId: 'report_2609393__conv-1',
                                        items: [
                                            {
                                                windowId: 'report_2656980__conv-1',
                                                conversationId: 'conv-1',
                                                windowKey: 'report',
                                                windowTitle: 'Report Summary',
                                                presentation: 'hosted',
                                                region: 'chat.top',
                                                parentKey: 'chat/new',
                                                inTab: true,
                                                parameters: { entityId: [2656980] },
                                            },
                                            {
                                                windowId: 'report_2609393__conv-1',
                                                conversationId: 'conv-1',
                                                windowKey: 'report',
                                                windowTitle: 'Report Summary',
                                                presentation: 'hosted',
                                                region: 'chat.top',
                                                parentKey: 'chat/new',
                                                inTab: true,
                                                parameters: { entityId: [2609393] },
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
                    windowId: 'report_2656980__conv-1',
                    conversationId: 'conv-1',
                    windowKey: 'report',
                    windowTitle: 'Report Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: { entityId: [2656980] },
                },
                {
                    windowId: 'report_2609393__conv-1',
                    conversationId: 'conv-1',
                    windowKey: 'report',
                    windowTitle: 'Report Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: { entityId: [2609393] },
                },
            ],
            selectedWindowId: 'report_2609393__conv-1',
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
                                            focusedWindowId: 'report_2609393__conv-1',
                                            items: [
                                                {
                                                    windowId: 'report_2656980__conv-1',
                                                    conversationId: 'conv-1',
                                                    windowKey: 'report',
                                                    windowTitle: 'Report Summary',
                                                    presentation: 'hosted',
                                                    region: 'chat.top',
                                                    parentKey: 'chat/new',
                                                    inTab: true,
                                                    parameters: { entityId: [2656980] },
                                                },
                                                {
                                                    windowId: 'report_2609393__conv-1',
                                                    conversationId: 'conv-1',
                                                    windowKey: 'report',
                                                    windowTitle: 'Report Summary',
                                                    presentation: 'hosted',
                                                    region: 'chat.top',
                                                    parentKey: 'chat/new',
                                                    inTab: true,
                                                    parameters: { entityId: [2609393] },
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
                                            windowId: 'report_2656980__conv-1',
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
                    windowId: 'report_2656980__conv-1',
                    conversationId: 'conv-1',
                    windowKey: 'report',
                    windowTitle: 'Report Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: { entityId: [2656980] },
                },
                {
                    windowId: 'report_2609393__conv-1',
                    conversationId: 'conv-1',
                    windowKey: 'report',
                    windowTitle: 'Report Summary',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: { entityId: [2609393] },
                },
            ],
            selectedWindowId: 'report_2656980__conv-1',
        });
    });

    it('merges completed ui/window:setFormData values into the opened hosted window form', () => {
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
                                    responsePayload: {
                                        windowId: 'reportBuilder__conv-1',
                                        conversationId: 'conv-1',
                                        windowKey: 'reportBuilder',
                                        windowTitle: 'Report Builder',
                                        presentation: 'hosted',
                                        region: 'chat.top',
                                        parentKey: 'chat/new',
                                        selectedWindowId: 'reportBuilder__conv-1',
                                        windowForm: {
                                            prefill: {
                                                accountId: 7,
                                            },
                                        },
                                    },
                                },
                                {
                                    toolName: 'ui/window:setFormData',
                                    status: 'completed',
                                    requestPayload: {
                                        windowId: 'reportBuilder__conv-1',
                                        values: {
                                            prefill: {
                                                recordId: 123,
                                            },
                                        },
                                    },
                                    responsePayload: {
                                        ok: true,
                                    },
                                },
                            ],
                        },
                    ],
                },
            } as any,
        ])).toEqual({
            windows: [
                {
                    windowId: 'reportBuilder__conv-1',
                    conversationId: 'conv-1',
                    windowKey: 'reportBuilder',
                    windowTitle: 'Report Builder',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: {},
                    windowForm: {
                        prefill: {
                            accountId: 7,
                            recordId: 123,
                        },
                    },
                },
            ],
            selectedWindowId: 'reportBuilder__conv-1',
        });
    });

    it('supports inline-body ui/window/setFormData payloads and replace mode', () => {
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
                                            id: 'reportBuilder',
                                            parameters: {},
                                        }),
                                        Compression: 'none',
                                    },
                                    responsePayload: {
                                        InlineBody: JSON.stringify({
                                            conversationId: 'conv-1',
                                            items: [
                                                {
                                                    conversationId: 'conv-1',
                                                    parentKey: 'chat/new',
                                                    presentation: 'hosted',
                                                    region: 'chat.top',
                                                    windowId: 'reportBuilder__conv-1',
                                                    windowKey: 'reportBuilder',
                                                    windowTitle: 'Report Builder',
                                                    windowForm: {
                                                        reportBuilder: {
                                                            reportDocumentTitle: 'Saved title',
                                                        },
                                                    },
                                                },
                                            ],
                                            selectedWindowId: 'reportBuilder__conv-1',
                                        }),
                                        Compression: 'none',
                                    },
                                },
                                {
                                    toolName: 'ui/window/setFormData',
                                    status: 'completed',
                                    requestPayload: {
                                        InlineBody: JSON.stringify({
                                            windowId: 'reportBuilder__conv-1',
                                            values: {
                                                prefill: {
                                                    country: ['US'],
                                                    recordIds: [123],
                                                },
                                            },
                                        }),
                                        Compression: 'none',
                                    },
                                    responsePayload: {
                                        ok: true,
                                    },
                                },
                                {
                                    toolName: 'ui/window/setFormData',
                                    status: 'completed',
                                    requestPayload: {
                                        windowId: 'reportBuilder__conv-1',
                                        values: {
                                            reportBuilder: {
                                                reportDocumentTitle: 'Replaced title',
                                            },
                                        },
                                        replace: true,
                                    },
                                    responsePayload: {
                                        ok: true,
                                    },
                                },
                            ],
                        },
                    ],
                },
            } as any,
        ])).toEqual({
            windows: [
                {
                    windowId: 'reportBuilder__conv-1',
                    conversationId: 'conv-1',
                    windowKey: 'reportBuilder',
                    windowTitle: 'Report Builder',
                    presentation: 'hosted',
                    region: 'chat.top',
                    parentKey: 'chat/new',
                    inTab: true,
                    parameters: {},
                    windowForm: {
                        reportBuilder: {
                            reportDocumentTitle: 'Replaced title',
                        },
                    },
                },
            ],
            selectedWindowId: 'reportBuilder__conv-1',
        });
    });
});
