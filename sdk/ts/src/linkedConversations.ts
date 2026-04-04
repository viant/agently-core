import type { ExecutionPage, ModelStepState, SSEEvent, ToolStepState, TranscriptOutput, Turn } from './types';

type LegacyModelStep = Partial<ModelStepState> & {
    Provider?: string;
    Model?: string;
    Status?: string;
};

type LegacyToolStep = Partial<ToolStepState> & {
    ToolName?: string;
    Status?: string;
};

type LegacyExecutionPage = Partial<ExecutionPage> & {
    AssistantMessageId?: string;
    PageId?: string;
    Preamble?: string;
    Content?: string;
    Status?: string;
    FinalResponse?: boolean;
    ToolSteps?: LegacyToolStep[];
    ModelSteps?: LegacyModelStep[];
    CompletedAt?: string;
    CreatedAt?: string;
};

type LegacyTurn = Partial<Turn> & {
    Status?: string;
    AgentIdUsed?: string;
    AgentId?: string;
    UpdatedAt?: string;
    CreatedAt?: string;
    Response?: { Content?: string };
    Execution?: { Pages?: LegacyExecutionPage[] };
};

type TranscriptLike = TranscriptOutput & {
    Turns?: LegacyTurn[];
};

export type LinkedConversationPreviewStep =
    ({ kind: 'model' } & Partial<ModelStepState>)
    | ({ kind: 'tool' } & Partial<ToolStepState>);

export interface LinkedConversationPreviewGroup {
    id: string;
    title: string;
    status: string;
    finalResponse: boolean;
    content: string;
    stepKind: string;
    stepLabel: string;
    detailStep: LinkedConversationPreviewStep | null;
    modelStep: ({ kind: 'model' } & Partial<ModelStepState>) | null;
    toolSteps: ({ kind: 'tool' } & Partial<ToolStepState>)[];
}

export interface LinkedConversationPreviewSummary {
    status: string;
    response: string;
    updatedAt: string;
    agentId: string;
    previewGroups: LinkedConversationPreviewGroup[];
}

function stepTitle(step: LinkedConversationPreviewStep | LegacyModelStep | LegacyToolStep | null = null): string {
    const kind = String(step?.kind || '').toLowerCase();
    if (kind === 'model') {
        const provider = String(step?.provider || step?.Provider || '').trim();
        const model = String(step?.model || step?.Model || '').trim();
        return model ? `${provider ? `${provider}/` : ''}${model}` : 'model';
    }
    return String(step?.toolName || step?.ToolName || 'tool').trim() || 'tool';
}

export function summarizeLinkedConversationTranscript(payload: TranscriptLike = {}): LinkedConversationPreviewSummary {
    const turns = Array.isArray(payload?.turns) ? payload.turns : (Array.isArray(payload?.Turns) ? payload.Turns : []);
    const lastTurn = turns[turns.length - 1] || null;
    if (!lastTurn) {
        return { status: '', response: '', updatedAt: '', agentId: '', previewGroups: [] };
    }
    const execution = lastTurn?.execution || lastTurn?.Execution || null;
    const pages = Array.isArray(execution?.pages) ? execution.pages : (Array.isArray(execution?.Pages) ? execution.Pages : []);
    const latestPage = [...pages].reverse().find((page) => {
        if (!page) return false;
        if (String(page?.content || page?.Content || '').trim()) return true;
        if (String(page?.preamble || page?.Preamble || '').trim()) return true;
        const toolSteps = Array.isArray(page?.toolSteps) ? page.toolSteps : (Array.isArray(page?.ToolSteps) ? page.ToolSteps : []);
        const modelSteps = Array.isArray(page?.modelSteps) ? page.modelSteps : (Array.isArray(page?.ModelSteps) ? page.ModelSteps : []);
        return toolSteps.length > 0 || modelSteps.length > 0;
    }) || null;
    const response = String(
        latestPage?.content
        || latestPage?.Content
        || latestPage?.preamble
        || latestPage?.Preamble
        || lastTurn?.response?.content
        || lastTurn?.Response?.Content
        || ''
    ).trim();
    const latestToolSteps = Array.isArray(latestPage?.toolSteps) ? latestPage.toolSteps : (Array.isArray(latestPage?.ToolSteps) ? latestPage.ToolSteps : []);
    const latestTool = latestToolSteps.length > 0 ? latestToolSteps[latestToolSteps.length - 1] : null;
    const previewGroups: LinkedConversationPreviewGroup[] = [...pages].slice(-3).map((page, index) => {
        const toolSteps = Array.isArray(page?.toolSteps) ? page.toolSteps : (Array.isArray(page?.ToolSteps) ? page.ToolSteps : []);
        const modelSteps = Array.isArray(page?.modelSteps) ? page.modelSteps : (Array.isArray(page?.ModelSteps) ? page.ModelSteps : []);
        const modelStep = modelSteps[0] || null;
        const primaryStep = toolSteps[toolSteps.length - 1] || modelStep || null;
        const normalizedModelStep = modelStep
            ? ({
                ...modelStep,
                kind: 'model',
                provider: String(modelStep?.provider || modelStep?.Provider || '').trim(),
                model: String(modelStep?.model || modelStep?.Model || '').trim(),
                status: String(modelStep?.status || modelStep?.Status || '').trim(),
            } as ({ kind: 'model' } & Partial<ModelStepState>))
            : null;
        const normalizedToolSteps = toolSteps.map((step) => (
            {
                ...step,
                kind: 'tool',
                toolName: String(step?.toolName || step?.ToolName || '').trim(),
                status: String(step?.status || step?.Status || '').trim(),
            } as ({ kind: 'tool' } & Partial<ToolStepState>)
        ));
        const title = String(page?.preamble || page?.Preamble || '').trim()
            || (toolSteps.length > 0 ? `Using ${stepTitle(toolSteps[toolSteps.length - 1])}.` : '')
            || (modelStep ? stepTitle(modelStep) : '')
            || `Step ${index + 1}`;
        return {
            id: String(page?.assistantMessageId || page?.AssistantMessageId || page?.pageId || page?.PageId || `preview:${index}`).trim(),
            title,
            status: String(page?.status || page?.Status || '').trim(),
            finalResponse: Boolean(page?.finalResponse || page?.FinalResponse),
            content: String(page?.content || page?.Content || '').trim(),
            stepKind: String(primaryStep?.kind || (toolSteps.length > 0 ? 'tool' : 'model')).trim(),
            stepLabel: primaryStep ? stepTitle(primaryStep) : '',
            detailStep: primaryStep ? ({ ...primaryStep } as LinkedConversationPreviewStep) : null,
            modelStep: normalizedModelStep,
            toolSteps: normalizedToolSteps,
        };
    }).filter((entry) => String(entry?.title || '').trim() !== '');
    return {
        status: String(lastTurn?.status || lastTurn?.Status || latestPage?.status || latestPage?.Status || '').trim(),
        response: response || (latestTool ? `Using ${stepTitle(latestTool)}.` : ''),
        updatedAt: String(latestPage?.completedAt || latestPage?.CompletedAt || latestPage?.createdAt || latestPage?.CreatedAt || lastTurn?.updatedAt || lastTurn?.UpdatedAt || lastTurn?.createdAt || lastTurn?.CreatedAt || '').trim(),
        agentId: String(lastTurn?.agentIdUsed || lastTurn?.AgentIdUsed || lastTurn?.agentId || lastTurn?.AgentId || '').trim(),
        previewGroups,
    };
}

export function reduceLinkedConversationPreviewEvent(current: Partial<LinkedConversationPreviewSummary> = {}, event: Partial<SSEEvent> = {}) {
    const next = { ...current };
    const type = String(event?.type || '').trim().toLowerCase();
    const content = String(event?.content || event?.preamble || '').trim();
    const status = String(event?.status || '').trim();
    const assistantMessageId = String(event?.assistantMessageId || '').trim();
    const toolName = String(event?.toolName || '').trim();

    if (type === 'text_delta') {
        next.response = `${String(next.response || '')}${String(event?.content || '')}`.trim();
        return next;
    }
    if (type === 'assistant_final') {
        next.response = content || String(next.response || '').trim();
        if (status) next.status = status;
        return next;
    }
    if (type === 'turn_completed' || type === 'turn_failed' || type === 'turn_canceled') {
        next.status = status || type.replace('turn_', '');
        return next;
    }
    if (type === 'model_started' || type === 'model_completed') {
        const previewGroups = Array.isArray(next.previewGroups) ? [...next.previewGroups] : [];
        const groupKey = assistantMessageId || `model:${previewGroups.length}`;
        const existingIndex = previewGroups.findIndex((item) => String(item?.id || '').trim() === groupKey);
        const merged = {
            id: groupKey,
            title: content || (type === 'model_started' ? 'Thinking…' : 'Model step'),
            status: status || (type === 'model_started' ? 'running' : 'completed'),
            finalResponse: type === 'model_completed' && !!event?.finalResponse,
            content: type === 'model_completed' ? content : '',
            stepKind: 'model',
            stepLabel: String(event?.modelName || event?.model?.model || '').trim(),
            detailStep: {
                kind: 'model',
                model: String(event?.modelName || event?.model?.model || '').trim(),
                provider: String(event?.provider || event?.model?.provider || '').trim(),
                status: status || '',
            },
            modelStep: {
                kind: 'model',
                model: String(event?.modelName || event?.model?.model || '').trim(),
                provider: String(event?.provider || event?.model?.provider || '').trim(),
                status: status || '',
            },
            toolSteps: [],
        };
        if (existingIndex === -1) previewGroups.push(merged);
        else previewGroups[existingIndex] = { ...previewGroups[existingIndex], ...merged };
        next.previewGroups = previewGroups.slice(-3);
        if (status) next.status = status;
        if (content) next.response = content;
        return next;
    }
    if (type === 'tool_call_started' || type === 'tool_call_completed') {
        const previewGroups = Array.isArray(next.previewGroups) ? [...next.previewGroups] : [];
        const groupKey = String(event?.toolCallId || event?.toolMessageId || toolName || `tool:${previewGroups.length}`).trim();
        const merged = {
            id: groupKey,
            title: toolName ? `Using ${toolName}.` : 'Tool step',
            status: status || (type === 'tool_call_started' ? 'running' : 'completed'),
            finalResponse: false,
            content: '',
            stepKind: 'tool',
            stepLabel: toolName,
            detailStep: {
                kind: 'tool',
                toolName,
                status: status || '',
            },
            modelStep: null,
            toolSteps: [{
                kind: 'tool',
                toolName,
                status: status || '',
            }],
        };
        const existingIndex = previewGroups.findIndex((item) => String(item?.id || '').trim() === groupKey);
        if (existingIndex === -1) previewGroups.push(merged);
        else previewGroups[existingIndex] = { ...previewGroups[existingIndex], ...merged };
        next.previewGroups = previewGroups.slice(-3);
        if (status) next.status = status;
        return next;
    }
    return next;
}
