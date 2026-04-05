import type {
    ApprovalCallbackPayload,
    ApprovalCallbackResult,
    ApprovalEditor,
    ApprovalMeta,
    JSONObject,
} from './types';

export interface ApprovalHandlerResolver {
    (handlerName: string): ((payload: ApprovalCallbackPayload & { callback?: { elementId?: string; event?: string; handler?: string } }) => Promise<ApprovalCallbackResult | void> | ApprovalCallbackResult | void) | null | undefined;
}

/**
 * Approval callback lifecycle contract:
 *
 * 1. Callbacks run in declaration order.
 * 2. A callback runs only when its `event` matches the requested event, or when
 *    the callback declares no event.
 * 3. `editedFields` are merged shallowly; later callbacks win on key conflicts.
 * 4. `action`, when returned, overrides the current action; later callbacks win.
 * 5. Missing handlers are skipped silently by the SDK. Handler resolution is an
 *    app concern, injected through `resolveHandler`.
 */

function normalizeApprovalOption(option: any) {
    const id = String(option?.id || '').trim();
    const label = String(option?.label || '').trim();
    if (!id || !label) return null;
    return {
        id,
        label,
        description: String(option?.description || '').trim(),
        item: option?.item,
        selected: option?.selected !== false,
    };
}

function normalizeApprovalEditor(editor: any): ApprovalEditor | null {
    const name = String(editor?.name || '').trim();
    if (!name) return null;
    return {
        name,
        kind: String(editor?.kind || 'checkbox_list').trim(),
        path: String(editor?.path || '').trim(),
        label: String(editor?.label || '').trim(),
        description: String(editor?.description || '').trim(),
        options: Array.isArray(editor?.options)
            ? editor.options.map(normalizeApprovalOption).filter(Boolean)
            : [],
    };
}

export function normalizeApprovalMeta(raw: unknown): ApprovalMeta | null {
    if (!raw) return null;
    let value: any = raw;
    if (typeof value === 'string') {
        try {
            value = JSON.parse(value);
        } catch {
            return null;
        }
    }
    if (!value || typeof value !== 'object') return null;
    if (value.approval && typeof value.approval === 'object') value = value.approval;
    const type = String(value.type || '').trim();
    if (type && type !== 'tool_approval') return null;
    return {
        type: 'tool_approval',
        toolName: String(value.toolName || '').trim(),
        title: String(value.title || '').trim(),
        message: String(value.message || '').trim(),
        acceptLabel: String(value.acceptLabel || '').trim(),
        rejectLabel: String(value.rejectLabel || '').trim(),
        cancelLabel: String(value.cancelLabel || '').trim(),
        forge: value.forge && typeof value.forge === 'object'
            ? {
                windowRef: String(value.forge.windowRef || '').trim(),
                containerRef: String(value.forge.containerRef || '').trim(),
                dataSource: String(value.forge.dataSource || '').trim(),
                callbacks: Array.isArray(value.forge.callbacks)
                    ? value.forge.callbacks.map((callback: any) => ({
                        elementId: String(callback?.elementId || '').trim(),
                        event: String(callback?.event || '').trim(),
                        handler: String(callback?.handler || '').trim(),
                    })).filter((callback: any) => callback.handler)
                    : [],
            }
            : undefined,
        editors: Array.isArray(value.editors)
            ? value.editors.map(normalizeApprovalEditor).filter(Boolean)
            : [],
    };
}

export function buildApprovalEditorState(meta: ApprovalMeta | null | undefined): Record<string, string | string[]> {
    const state: Record<string, string | string[]> = {};
    const editors = Array.isArray(meta?.editors) ? meta.editors : [];
    for (const editor of editors) {
        if (!editor?.name) continue;
        const selected = Array.isArray(editor.options)
            ? editor.options.filter((option) => option?.selected !== false).map((option) => option.id)
            : [];
        if (String(editor.kind || '').toLowerCase() === 'radio_list') {
            state[editor.name] = selected[0] || '';
        } else {
            state[editor.name] = selected;
        }
    }
    return state;
}

export function serializeApprovalEditedFields(meta: ApprovalMeta | null | undefined, state: Record<string, unknown> = {}): JSONObject {
    const editedFields: JSONObject = {};
    const editors = Array.isArray(meta?.editors) ? meta.editors : [];
    for (const editor of editors) {
        if (!editor?.name) continue;
        const kind = String(editor.kind || '').toLowerCase();
        if (kind === 'radio_list') {
            editedFields[editor.name] = String(state?.[editor.name] || '').trim();
            continue;
        }
        const values = Array.isArray(state?.[editor.name]) ? state[editor.name] as unknown[] : [];
        editedFields[editor.name] = values.map((value) => String(value || '').trim()).filter(Boolean);
    }
    return editedFields;
}

export function buildApprovalForgeSchema(meta: ApprovalMeta | null | undefined): JSONObject {
    const properties: JSONObject = {};
    const required: string[] = [];
    const editors = Array.isArray(meta?.editors) ? meta.editors : [];
    for (const editor of editors) {
        if (!editor?.name) continue;
        const kind = String(editor.kind || '').toLowerCase();
        const options = Array.isArray(editor.options) ? editor.options : [];
        const values = options.map((option) => option.id);
        if (kind === 'radio_list') {
            properties[editor.name] = {
                type: 'string',
                title: editor.label || editor.name,
                description: editor.description || '',
                enum: values,
                default: values.find((value, index) => options[index]?.selected !== false) || '',
                'x-ui-widget': 'radio',
            };
            continue;
        }
        properties[editor.name] = {
            type: 'array',
            title: editor.label || editor.name,
            description: editor.description || '',
            enum: values,
            default: values.filter((value, index) => options[index]?.selected !== false),
            'x-ui-widget': 'multiSelect',
        };
    }
    return {
        type: 'object',
        properties,
        required,
    };
}

function mergeCallbackResult(base: ApprovalCallbackPayload, result: ApprovalCallbackResult | void | null | undefined): ApprovalCallbackPayload {
    if (!result || typeof result !== 'object') return base;
    const merged: ApprovalCallbackPayload = { ...base };
    if (result.editedFields && typeof result.editedFields === 'object') {
        merged.editedFields = {
            ...(merged.editedFields || {}),
            ...result.editedFields,
        };
    }
    if (typeof result.action === 'string' && result.action.trim()) {
        merged.action = result.action.trim();
    }
    return merged;
}

export async function executeApprovalCallbacks(args: {
    meta?: ApprovalMeta | null;
    event: string;
    payload: ApprovalCallbackPayload;
    resolveHandler?: ApprovalHandlerResolver | null;
}): Promise<ApprovalCallbackPayload> {
    const { meta, event, resolveHandler } = args || {};
    const callbacks = Array.isArray(meta?.forge?.callbacks) ? meta!.forge!.callbacks! : [];
    if (!callbacks.length || typeof resolveHandler !== 'function') return args.payload;

    let nextPayload: ApprovalCallbackPayload = { ...(args.payload || {}) };
    for (const callback of callbacks) {
        const callbackEvent = String(callback?.event || '').trim().toLowerCase();
        if (callbackEvent && callbackEvent !== String(event || '').trim().toLowerCase()) continue;
        const handlerName = String(callback?.handler || '').trim();
        if (!handlerName) continue;
        const fn = resolveHandler(handlerName);
        if (typeof fn !== 'function') continue;
        // eslint-disable-next-line no-await-in-loop
        const result = await fn({
            ...nextPayload,
            callback: {
                elementId: String(callback?.elementId || '').trim(),
                event: String(callback?.event || '').trim(),
                handler: handlerName,
            },
        });
        nextPayload = mergeCallbackResult(nextPayload, result);
    }
    return nextPayload;
}
