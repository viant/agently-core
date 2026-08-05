import type { Turn } from './types';

export interface WorkspaceWindowSnapshot {
    windowId: string;
    conversationId?: string | null;
    windowKey: string;
    windowTitle?: string | null;
    presentation?: string | null;
    region?: string | null;
    parentKey?: string | null;
    workspaceSharePct?: number | null;
    workspaceMinHeight?: number | null;
    inTab?: boolean;
    parameters?: Record<string, unknown>;
    windowForm?: Record<string, unknown>;
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
    const parentKey = String(raw.parentKey || '').trim();
    const windowId = String(raw.windowId || '').trim();
    const windowKey = String(raw.windowKey || '').trim();
    if (!windowId || !windowKey) return null;
    const parameters = raw.parameters && typeof raw.parameters === 'object'
        ? raw.parameters as Record<string, unknown>
        : {};
    const windowForm = raw.windowForm && typeof raw.windowForm === 'object'
        ? raw.windowForm as Record<string, unknown>
        : undefined;
    return {
        windowId,
        conversationId: String(raw.conversationId || '').trim() || null,
        windowKey,
        windowTitle: String(raw.windowTitle || '').trim() || windowKey,
        presentation: raw.presentation || null,
        region: raw.region || null,
        parentKey,
        workspaceSharePct: Number.isFinite(Number(raw.workspaceSharePct)) ? Number(raw.workspaceSharePct) : undefined,
        workspaceMinHeight: Number.isFinite(Number(raw.workspaceMinHeight)) ? Number(raw.workspaceMinHeight) : undefined,
        inTab: raw.inTab !== false,
        parameters,
        windowForm,
    };
}

function hostedWorkspaceWindowsFromListPayload(raw: unknown): WorkspaceWindowSnapshot[] {
    const payload = firstParsedPayload(raw);
    const items = Array.isArray(payload?.items) ? payload.items : [];
    return items
        .map((item) => normalizeHostedWorkspaceWindow(item))
        .filter((item): item is WorkspaceWindowSnapshot => !!item);
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
    return !!value && typeof value === 'object' && !Array.isArray(value);
}

function cloneValue<T>(value: T): T {
    return value == null ? value : JSON.parse(JSON.stringify(value));
}

function mergeWindowFormValue(base: unknown, patch: unknown): unknown {
    if (patch === undefined) return cloneValue(base);
    if (base === undefined) return cloneValue(patch);
    if (Array.isArray(base) || Array.isArray(patch)) return cloneValue(patch);
    if (!isPlainObject(base) || !isPlainObject(patch)) return cloneValue(patch);
    const merged: Record<string, unknown> = { ...base };
    const keys = new Set([...Object.keys(base), ...Object.keys(patch)]);
    keys.forEach((key) => {
        merged[key] = mergeWindowFormValue(base[key], patch[key]);
    });
    return merged;
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
    const responsePayload = firstParsedPayload(step?.responsePayload, step?.content);
    const requestPayload = firstParsedPayload(step?.requestPayload);
    const items = Array.isArray(responsePayload?.items) ? responsePayload.items : [];
    if (items.length > 0) {
        return items
            .map((item) => normalizeHostedWorkspaceWindow(item))
            .filter((item): item is WorkspaceWindowSnapshot => !!item);
    }
    const normalized = normalizeHostedWorkspaceWindow({
        windowId: String(responsePayload?.windowId || '').trim(),
        conversationId: String(responsePayload?.conversationId || '').trim() || null,
        windowKey: String(responsePayload?.windowKey || requestPayload?.id || requestPayload?.windowKey || '').trim(),
        windowTitle: String(responsePayload?.windowTitle || '').trim(),
        presentation: String(responsePayload?.presentation || '').trim(),
        region: String(responsePayload?.region || '').trim(),
        parentKey: String(responsePayload?.parentKey || '').trim(),
        workspaceSharePct: Number.isFinite(Number(responsePayload?.workspaceSharePct))
            ? Number(responsePayload?.workspaceSharePct)
            : undefined,
        workspaceMinHeight: Number.isFinite(Number(responsePayload?.workspaceMinHeight))
            ? Number(responsePayload?.workspaceMinHeight)
            : undefined,
        inTab: responsePayload?.inTab !== false,
        parameters: requestPayload?.parameters && typeof requestPayload.parameters === 'object' ? requestPayload.parameters : {},
        windowForm: responsePayload?.windowForm && typeof responsePayload.windowForm === 'object'
            ? responsePayload.windowForm
            : undefined,
    });
    return normalized ? [normalized] : [];
}

function targetHostedWorkspaceWindows(windows: WorkspaceWindowSnapshot[], requestPayload: any): WorkspaceWindowSnapshot[] {
    const targetWindowId = String(requestPayload?.windowId || '').trim();
    if (targetWindowId) {
        return windows.filter((window) => String(window?.windowId || '').trim() === targetWindowId);
    }
    const targetWindowKey = String(requestPayload?.windowKey || '').trim();
    if (!targetWindowKey) {
        return [];
    }
    const matches = windows.filter((window) => String(window?.windowKey || '').trim() === targetWindowKey);
    return matches.length === 1 ? matches : [];
}

function applySetFormDataSteps(toolSteps: any[], windows: WorkspaceWindowSnapshot[], baseIndex: number): WorkspaceWindowSnapshot[] {
    const resolved = windows.map((window) => ({
        ...window,
        ...(window.windowForm && isPlainObject(window.windowForm)
            ? { windowForm: cloneValue(window.windowForm) as Record<string, unknown> }
            : {}),
    }));
    for (let index = Math.max(baseIndex + 1, 0); index < toolSteps.length; index += 1) {
        const step = toolSteps[index] || {};
        if (String(step?.status || '').trim().toLowerCase() !== 'completed') continue;
        const toolName = normalizeToolName(step?.toolName);
        if (toolName !== 'ui/window/setformdata') continue;
        const requestPayload = firstParsedPayload(step?.requestPayload);
        const responsePayload = firstParsedPayload(step?.responsePayload, step?.content);
        const authoritativeWindowForm = responsePayload?.windowForm;
        const values = isPlainObject(authoritativeWindowForm)
            ? authoritativeWindowForm
            : requestPayload?.values;
        if (!isPlainObject(values)) continue;
        const replace = isPlainObject(authoritativeWindowForm) || requestPayload?.replace === true;
        const targetPayload = responsePayload?.windowId || responsePayload?.windowKey
            ? responsePayload
            : requestPayload;
        const targets = targetHostedWorkspaceWindows(resolved, targetPayload);
        if (targets.length === 0) continue;
        targets.forEach((window) => {
            window.windowForm = replace
                ? cloneValue(values) as Record<string, unknown>
                : mergeWindowFormValue(window.windowForm, values) as Record<string, unknown>;
        });
    }
    return resolved;
}

export function deriveHostedWorkspaceRestoreStateFromTranscriptTurns(turns: Turn[] = []): HostedWorkspaceRestoreState | null {
    const list = Array.isArray(turns) ? turns : [];
    const toolSteps = toolStepsForTurn(list[list.length - 1]);
    if (toolSteps.length === 0) return null;
    for (let i = toolSteps.length - 1; i >= 0; i -= 1) {
        const step = toolSteps[i] || {};
        if (String(step?.status || '').trim().toLowerCase() !== 'completed') continue;
        const toolName = normalizeToolName(step?.toolName);
        if (toolName === 'ui/window/list') {
            const windows = applySetFormDataSteps(
                toolSteps,
                hostedWorkspaceWindowsFromListPayload(firstParsedPayload(step?.responsePayload, step?.content)),
                i,
            );
            if (windows.length === 0) continue;
            return {
                windows,
                selectedWindowId: selectedWindowIdFromToolSteps(toolSteps, windows) || null,
            };
        }
        if (toolName === 'ui/view/open' || toolName === 'ui/window/open') {
            const windows = applySetFormDataSteps(toolSteps, hostedWorkspaceWindowsFromViewOpenStep(step), i);
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
    return null;
}
