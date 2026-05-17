import type { Turn } from './types';

export interface WorkspaceWindowSnapshot {
    windowId: string;
    conversationId?: string | null;
    windowKey: string;
    windowTitle?: string | null;
    presentation?: string | null;
    region?: string | null;
    parentKey?: string | null;
    inTab?: boolean;
    parameters?: Record<string, unknown>;
}

export interface HostedWorkspaceRestoreState {
    windows: WorkspaceWindowSnapshot[];
    selectedWindowId?: string | null;
}

function parsePayload(raw: unknown): any {
    if (!raw) return null;
    if (typeof raw === 'string') {
        try {
            return JSON.parse(raw);
        } catch {
            return null;
        }
    }
    if (typeof raw === 'object') {
        const inlineBody = (raw as any)?.inlineBody ?? (raw as any)?.InlineBody;
        if (typeof inlineBody === 'string') {
            try {
                return JSON.parse(inlineBody);
            } catch {
                return raw;
            }
        }
        return raw;
    }
    return null;
}

function isPayloadEnvelope(value: any): boolean {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
    const hasInlineBody = typeof value.inlineBody === 'string' || typeof value.InlineBody === 'string';
    const hasCompression = typeof value.compression === 'string' || typeof value.Compression === 'string';
    const hasDirectWorkspaceShape = 'items' in value || 'windowId' in value || 'focusedWindowId' in value;
    return (hasInlineBody || hasCompression) && !hasDirectWorkspaceShape;
}

function firstParsedPayload(...candidates: unknown[]): any {
    for (const candidate of candidates) {
        const parsed = parsePayload(candidate);
        if (isPayloadEnvelope(parsed)) continue;
        if (parsed && typeof parsed === 'object') {
            return parsed;
        }
    }
    return null;
}

function normalizeToolName(raw: unknown): string {
    return String(raw || '').trim().toLowerCase().replace(/:/g, '/');
}

function toolStepsForTurn(turn: Turn | null | undefined): any[] {
    const currentTurn = turn as any;
    if (!currentTurn || typeof currentTurn !== 'object') return [];
    const pages = Array.isArray(currentTurn?.execution?.pages) ? currentTurn.execution.pages : [];
    const result: any[] = [];
    for (const page of pages) {
        const toolSteps = Array.isArray(page?.toolSteps) ? page.toolSteps : [];
        for (const step of toolSteps) {
            result.push(step || {});
        }
    }
    return result;
}

function normalizeHostedWorkspaceWindow(raw: any): WorkspaceWindowSnapshot | null {
    if (!raw || typeof raw !== 'object') return null;
    const presentation = String(raw.presentation || '').trim().toLowerCase();
    const region = String(raw.region || '').trim().toLowerCase();
    const parentKey = String(raw.parentKey || '').trim();
    const windowId = String(raw.windowId || '').trim();
    const windowKey = String(raw.windowKey || '').trim();
    if (!windowId || !windowKey) return null;
    if (presentation !== 'hosted') return null;
    if (region !== 'chat.top') return null;
    if (parentKey !== 'chat/new') return null;
    const parameters = raw.parameters && typeof raw.parameters === 'object'
        ? raw.parameters as Record<string, unknown>
        : {};
    return {
        windowId,
        conversationId: String(raw.conversationId || '').trim() || null,
        windowKey,
        windowTitle: String(raw.windowTitle || '').trim() || windowKey,
        presentation: raw.presentation || null,
        region: raw.region || null,
        parentKey,
        inTab: raw.inTab !== false,
        parameters,
    };
}

function hostedWorkspaceWindowsFromListPayload(raw: unknown): WorkspaceWindowSnapshot[] {
    const payload = firstParsedPayload(raw);
    const items = Array.isArray(payload?.items) ? payload.items : [];
    return items
        .map((item) => normalizeHostedWorkspaceWindow(item))
        .filter((item): item is WorkspaceWindowSnapshot => !!item);
}

function selectedWindowIdFromToolSteps(toolSteps: any[], windows: WorkspaceWindowSnapshot[]): string {
    const windowIds = new Set(windows.map((window) => String(window.windowId || '').trim()).filter(Boolean));
    if (windowIds.size === 0) return '';
    for (let i = toolSteps.length - 1; i >= 0; i -= 1) {
        const step = toolSteps[i] || {};
        if (String(step?.status || '').trim().toLowerCase() !== 'completed') continue;
        const toolName = normalizeToolName(step?.toolName);
        if (toolName === 'ui/window/show') {
            const requestPayload = parsePayload(step?.requestPayload);
            const windowId = String(requestPayload?.windowId || '').trim();
            if (windowIds.has(windowId)) return windowId;
        }
        if (toolName === 'ui/window/list') {
            const responsePayload = firstParsedPayload(step?.responsePayload, step?.content);
            const focusedWindowId = String(responsePayload?.focusedWindowId || '').trim();
            if (windowIds.has(focusedWindowId)) return focusedWindowId;
        }
    }
    return '';
}

function hostedWorkspaceWindowsFromViewOpenStep(step: any): WorkspaceWindowSnapshot[] {
    const toolName = normalizeToolName(step?.toolName);
    const responsePayload = firstParsedPayload(step?.responsePayload, step?.content);
    const requestPayload = firstParsedPayload(step?.requestPayload);
    if (toolName !== 'ui/view/open') return [];
    const items = Array.isArray(responsePayload?.items) ? responsePayload.items : [];
    if (items.length > 0) {
        return items
            .map((item) => normalizeHostedWorkspaceWindow(item))
            .filter((item): item is WorkspaceWindowSnapshot => !!item);
    }
    const normalized = normalizeHostedWorkspaceWindow({
        windowId: String(responsePayload?.windowId || '').trim(),
        conversationId: String(responsePayload?.conversationId || '').trim() || null,
        windowKey: String(responsePayload?.windowKey || requestPayload?.id || '').trim(),
        windowTitle: String(responsePayload?.windowTitle || '').trim(),
        presentation: String(responsePayload?.presentation || '').trim(),
        region: String(responsePayload?.region || '').trim(),
        parentKey: String(responsePayload?.parentKey || '').trim(),
        inTab: true,
        parameters: requestPayload?.parameters && typeof requestPayload.parameters === 'object' ? requestPayload.parameters : {},
    });
    return normalized ? [normalized] : [];
}

export function deriveHostedWorkspaceRestoreStateFromTranscriptTurns(turns: Turn[] = []): HostedWorkspaceRestoreState | null {
    const list = Array.isArray(turns) ? turns : [];
    for (let turnIndex = list.length - 1; turnIndex >= 0; turnIndex -= 1) {
        const toolSteps = toolStepsForTurn(list[turnIndex]);
        if (toolSteps.length === 0) continue;
        for (let i = toolSteps.length - 1; i >= 0; i -= 1) {
            const step = toolSteps[i] || {};
            if (String(step?.status || '').trim().toLowerCase() !== 'completed') continue;
            const toolName = normalizeToolName(step?.toolName);
            if (toolName === 'ui/window/list') {
                const windows = hostedWorkspaceWindowsFromListPayload(firstParsedPayload(step?.responsePayload, step?.content));
                if (windows.length === 0) continue;
                return {
                    windows,
                    selectedWindowId: selectedWindowIdFromToolSteps(toolSteps, windows) || null,
                };
            }
            if (toolName === 'ui/view/open') {
                const windows = hostedWorkspaceWindowsFromViewOpenStep(step);
                if (windows.length === 0) continue;
                const responsePayload = firstParsedPayload(step?.responsePayload, step?.content);
                const selectedWindowId = String(responsePayload?.selectedWindowId || '').trim()
                    || String(windows[windows.length - 1]?.windowId || '').trim();
                return {
                    windows,
                    selectedWindowId: selectedWindowId || null,
                };
            }
        }
    }
    return null;
}
