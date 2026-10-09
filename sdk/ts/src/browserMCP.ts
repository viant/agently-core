import type { Tool, ToolCall } from '@ag-ui/core';
import type { ClientTool, ClientToolResult } from './agui';

export interface BrowserMCPDescriptor {
    name: string;
    executionLocation: 'browser';
    transport: { type: 'chrome-extension'; extensionId: string; portName: string; requestTimeoutMs?: number };
    allowedTools: string[];
}
export interface BrowserMCPConnection {
    request(method: 'initialize' | 'tools/list' | 'tools/call' | 'resources/read' | 'ping', params?: Record<string, unknown>): Promise<any>;
    close(): Promise<void> | void;
    onclose?: () => void;
}
export interface BrowserMCPCatalog {
    id: string; hash: string; connectionId: string; conversationId: string; threadId: string; server: string;
    tools: Tool[]; resources?: boolean; expiresAt: string;
}
export interface BrowserMCPHostOptions {
    transportFactory?: (descriptor: BrowserMCPDescriptor, binding: { conversationId: string; threadId: string; connectionId: string }) => BrowserMCPConnection;
    onApprovalRequired?: (server: string) => void;
}
interface BrowserMCPAPI {
    descriptors(): Promise<BrowserMCPDescriptor[]>;
    register(input: { conversationId: string; threadId: string; server: string; connectionId: string; tools: unknown[]; resources?: boolean }): Promise<BrowserMCPCatalog>;
    revoke(id: string): Promise<unknown>;
    current(id: string): Promise<BrowserMCPCatalog>;
}
type ChromePort = { postMessage(value: unknown): void; disconnect(): void; onMessage: { addListener(callback: (value: any) => void): void }; onDisconnect: { addListener(callback: () => void): void } };
type ChromeRuntime = { connect(extensionId: string, options: { name: string }): ChromePort; lastError?: unknown };

/** Generic extension MCP envelope adapter: public addressing only, no HTTP,
 * workspace cookie, OAuth token, native-host name or tool-specific code. */
export class ChromeExtensionMCPConnection implements BrowserMCPConnection {
    onclose?: () => void;
    private port!: ChromePort;
    private ready: Promise<void>;
    private rejectReady!: (error: Error) => void;
    private pending = new Map<string, { resolve(value: unknown): void; reject(error: Error): void; timer: ReturnType<typeof setTimeout> }>();
    private closed = false;
    private queue: Promise<unknown> = Promise.resolve();
    private queued = 0;
    private timeout: number;
    constructor(descriptor: BrowserMCPDescriptor, onApprovalRequired?: () => void, runtime: ChromeRuntime | undefined = (globalThis as any).chrome?.runtime) {
        const config = descriptor.transport;
        if (config.type !== 'chrome-extension' || !/^[a-p]{32}$/.test(config.extensionId) || !/^[A-Za-z0-9._:-]{1,128}$/.test(config.portName) || !runtime?.connect) throw new Error('Browser MCP transport unavailable');
        this.timeout = config.requestTimeoutMs || 9000;
        if (!Number.isSafeInteger(this.timeout) || this.timeout < 100 || this.timeout > 9000) throw new Error('Invalid browser MCP deadline');
        this.ready = new Promise((resolve, reject) => {
            this.rejectReady = reject;
            this.port = runtime.connect(config.extensionId, { name: config.portName });
            this.port.onMessage.addListener(message => {
                if (message?.type === 'approvalRequired') { onApprovalRequired?.(); return; }
                if (message?.type === 'approved') { resolve(); return; }
                if (message?.type !== 'result') return;
                const pending = this.pending.get(message.requestId); if (!pending) return;
                this.pending.delete(message.requestId); clearTimeout(pending.timer);
                if (message.result && JSON.stringify(message.result).length <= 256 * 1024) pending.resolve(message.result);
                else pending.reject(new Error('Browser MCP request unavailable; inspect the original operation before retrying'));
            });
            this.port.onDisconnect.addListener(() => { void runtime.lastError; this.finish(); });
        });
        this.ready.catch(() => undefined);
    }
    async request(method: 'initialize' | 'tools/list' | 'tools/call' | 'resources/read' | 'ping', params?: Record<string, unknown>): Promise<any> {
        if (this.closed || this.queued >= 16) throw new Error('Browser MCP connection unavailable');
        this.queued++;
        const run = this.queue.then(async () => {
            await this.ready;
            if (this.closed) throw new Error('Browser MCP connection closed');
            const requestId = crypto.randomUUID();
            const request = { type: 'request', requestId, operation: method, deadlineUnixMs: Date.now() + this.timeout, ...(params ? { params } : {}) };
            if (new TextEncoder().encode(JSON.stringify(request)).length > 65536) throw new Error('Browser MCP request exceeds transport budget');
            return new Promise((resolve, reject) => {
                const timer = setTimeout(() => { this.pending.delete(requestId); reject(new Error('Browser MCP deadline exceeded; do not replay uncertain work')); }, this.timeout);
                this.pending.set(requestId, { resolve, reject, timer });
                try { this.port.postMessage(request); } catch { clearTimeout(timer); this.pending.delete(requestId); reject(new Error('Browser MCP transport unavailable')); }
            });
        });
        this.queue = run.catch(() => undefined);
        try { return await run; } finally { this.queued--; }
    }
    private finish() {
        if (this.closed) return; this.closed = true;
        this.rejectReady(new Error('Browser MCP connection closed'));
        for (const p of this.pending.values()) { clearTimeout(p.timer); p.reject(new Error('Browser MCP connection lost; do not replay uncertain work')); }
        this.pending.clear(); this.onclose?.();
    }
    close() { try { this.port.disconnect(); } finally { this.finish(); } }
}
interface Binding {
    conversationId: string; threadId: string; config: string; connectionId: string; connection: BrowserMCPConnection;
    resources?: boolean; apps: Map<string, BrowserMCPAppActivity>; closed: boolean; revoked?: boolean; ready?: Promise<void>; origin: string; catalog?: BrowserMCPCatalog; tools: ClientTool[]; attempts: Map<string, { fingerprint: string; result?: ClientToolResult }>;
}
/** Binds catalogs and results to one authenticated API client and conversation.
 * Schemas are metadata; configured server/name policy plus the native executor
 * independently authorize every call. No result is relayed to another thread. */
export interface BrowserMCPAppActivity {
    mountKey: string;
    content: { resourceUri: string; result: any; toolInput: unknown; serverId: string; serverHash: string;
        _agentlyApp: { appInstanceId: string; threadId: string; nativeTurnId: string };
        _browserMCP: { catalogId: string; connectionId: string; callId: string; conversationId: string } };
}
function toolAudience(tool: Tool, audience: 'model' | 'app'): boolean {
    const visibility = (tool.metadata as any)?.ui?.visibility;
    return visibility === undefined || Array.isArray(visibility) && visibility.every(item => item === 'model' || item === 'app') && visibility.includes(audience);
}
export class BrowserMCPHost {
    private bindings = new Map<string, Binding>();
    private generation = 0;
    private appListeners = new Set<() => void>();
    constructor(private api: BrowserMCPAPI, private options: BrowserMCPHostOptions = {}) {}
    async tools(conversationId: string, threadId: string): Promise<ClientTool[]> {
        const generation = this.generation;
        const descriptors = await this.api.descriptors();
        if (generation !== this.generation) throw new Error('Browser MCP identity changed');
        const wanted = new Set(descriptors.filter(item => item.executionLocation === 'browser').map(item => item.name));
        for (const [key, binding] of this.bindings) if (binding.conversationId === conversationId && !wanted.has(JSON.parse(binding.config).name)) await this.closeBinding(key, binding);
        const result: ClientTool[] = [];
        for (const descriptor of descriptors) {
            if (descriptor.executionLocation !== 'browser') continue;
            const key = `${conversationId}\0${descriptor.name}`, config = JSON.stringify(descriptor);
            let binding = this.bindings.get(key);
            if (binding && (binding.closed || binding.threadId !== threadId || binding.origin !== (globalThis.location?.origin ?? '') || binding.config !== config || binding.catalog && Date.parse(binding.catalog.expiresAt) <= Date.now())) { await this.closeBinding(key, binding); binding = undefined; }
            if (!binding) {
                const connectionId = crypto.randomUUID();
                const connection = this.options.transportFactory?.(descriptor, { conversationId, threadId, connectionId }) ?? new ChromeExtensionMCPConnection(descriptor, () => this.options.onApprovalRequired?.(descriptor.name));
                binding = { conversationId, threadId, config, connectionId, connection, origin: globalThis.location?.origin ?? '', closed: false, apps: new Map(), tools: [], attempts: new Map() };
                this.bindings.set(key, binding);
                const active = binding;
                connection.onclose = () => { void this.closeBinding(key, active); };
                active.ready = (async () => { try {
                    const initialized = await connection.request('initialize', { capabilities: { extensions: { 'io.modelcontextprotocol/ui': { mimeTypes: ['text/html;profile=mcp-app'] } } } });
                    active.resources = !!initialized?.capabilities?.resources;
                    const catalog = await connection.request('tools/list');
                    if (active.closed || generation !== this.generation || !Array.isArray(catalog?.tools) || catalog.tools.length > 64) throw new Error('Browser MCP catalog unavailable');
                    const allowed = (name: string) => descriptor.allowedTools.some(pattern => new RegExp('^' + pattern.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*').replace(/\?/g, '.') + '$').test(name));
                    const tools = catalog.tools.filter((tool: any) => typeof tool?.name === 'string' && allowed(tool.name)).map((tool: any) => ({ name: tool.name, description: tool.description || '', inputSchema: tool.inputSchema, ...(tool._meta ? { _meta: tool._meta } : {}) }));
                    active.catalog = await this.api.register({ conversationId, threadId, server: descriptor.name, connectionId, tools, ...(active.resources ? { resources: true } : {}) });
                    if (active.closed || generation !== this.generation || active.catalog.connectionId !== connectionId || active.catalog.conversationId !== conversationId || active.catalog.threadId !== threadId) throw new Error('Browser MCP catalog binding changed');
                    active.tools = active.catalog.tools.filter(tool => toolAudience(tool, 'model')).map(tool => ({ tool, execute: (args, call) => this.call(active, tool, args, call) }));
                } catch (error) { await this.closeBinding(key, active); throw error; } })();
            }
            if (binding.ready) await binding.ready;
            result.push(...binding.tools);
        }
        return result;
    }
    private async call(binding: Binding, tool: Tool, args: unknown, call: ToolCall): Promise<ClientToolResult> {
        const metadata = tool.metadata as any;
        const identity = metadata?.browserMCP;
        if (binding.closed || binding.origin !== (globalThis.location?.origin ?? '') || !binding.catalog || Date.parse(binding.catalog.expiresAt) <= Date.now() || call.function.name !== tool.name || identity?.catalogId !== binding.catalog.id || identity?.connectionId !== binding.connectionId) throw new Error('Original browser MCP catalog is unavailable');
        const current = await this.api.current(binding.catalog.id);
        if (binding.closed || current.hash !== binding.catalog.hash || current.connectionId !== binding.connectionId || current.threadId !== binding.threadId) throw new Error('Browser MCP catalog was revoked or changed');
        const boundArgs = JSON.parse(call.function.arguments);
        if (JSON.stringify(boundArgs) !== JSON.stringify(args)) throw new Error('Browser MCP arguments differ from the pending call');
        const fingerprint = JSON.stringify([binding.conversationId, binding.threadId, binding.connectionId, tool.name, call.function.arguments]);
        const previous = binding.attempts.get(call.id);
        if (previous) { if (previous.fingerprint !== fingerprint || !previous.result) throw new Error('Browser MCP call is conflicting or uncertain; it will not be replayed'); return structuredClone(previous.result); }
        if (binding.attempts.size >= 256) throw new Error('Browser MCP call quota exhausted');
        binding.attempts.set(call.id, { fingerprint });
        const result = await binding.connection.request('tools/call', { name: identity.tool, arguments: boundArgs });
        if (binding.closed || !result || !Array.isArray(result.content)) throw new Error('Browser MCP result unavailable for the original conversation');
        // MCP _meta is host/UI-only and must not become model content.
        const content = JSON.stringify(result.structuredContent ?? result.content);
        if (new TextEncoder().encode(content).length > 192 * 1024) throw new Error('Browser MCP result exceeds relay budget');
        const answer: ClientToolResult = { content, ...(result.isError ? { error: 'Browser MCP tool returned an error' } : {}), metadata: structuredClone(tool.metadata) };
        binding.attempts.set(call.id, { fingerprint, result: structuredClone(answer) });
        const resourceUri = metadata?.ui?.resourceUri;
        if (typeof resourceUri === 'string' && resourceUri.startsWith('ui://')) {
            const appInstanceId = crypto.randomUUID();
            binding.apps.set(call.id, { mountKey: appInstanceId, content: { resourceUri, result: structuredClone(result), toolInput: boundArgs,
                serverId: binding.catalog.server, serverHash: binding.catalog.hash,
                _agentlyApp: { appInstanceId, threadId: binding.threadId, nativeTurnId: '' },
                _browserMCP: { catalogId: binding.catalog.id, connectionId: binding.connectionId, callId: call.id, conversationId: binding.conversationId } } });
            for (const listener of this.appListeners) listener();
        }
        return answer;
    }
    private async closeBinding(key: string, binding: Binding) {
        const wasClosed = binding.closed; binding.closed = true; binding.attempts.clear(); binding.apps.clear();
        for (const listener of this.appListeners) listener();
        if (this.bindings.get(key) === binding) this.bindings.delete(key);
        if (!wasClosed) { try { await binding.connection.close(); } catch { /* Revoke even if transport close failed. */ } }
        if (binding.catalog && !binding.revoked) { binding.revoked = true; await this.api.revoke(binding.catalog.id).catch(() => undefined); }
    }
    subscribeApps(listener: () => void) { this.appListeners.add(listener); return () => { this.appListeners.delete(listener); }; }
    appActivities(conversationId: string): BrowserMCPAppActivity[] {
        return [...this.bindings.values()].filter(binding => !binding.closed && binding.conversationId === conversationId).flatMap(binding => [...binding.apps.values()].map(app => structuredClone(app)));
    }
    appProxy(activity: BrowserMCPAppActivity) {
        const identity = activity.content._browserMCP;
        const binding = [...this.bindings.values()].find(item => item.connectionId === identity.connectionId && item.catalog?.id === identity.catalogId);
        const original = binding?.apps.get(identity.callId);
        if (!binding || !original || original.mountKey !== activity.mountKey || JSON.stringify(original.content) !== JSON.stringify(activity.content)) throw new Error('Browser app instance unavailable');
        let disposed = false;
        return { serverTools: false,
            call: async (method: string, params: Record<string, unknown> = {}) => {
                if (disposed || binding.closed || binding.origin !== (globalThis.location?.origin ?? '')) throw new Error('Browser app connection unavailable');
                // Local discovery cannot grant guest execution. Browser Apps
                // advertise no serverTools until an approval-aware route exists.
                if (method !== 'resources/read' || params.uri !== original.content.resourceUri) throw new Error('Browser app method outside resource binding');
                if (!binding.resources) throw new Error('Browser MCP server does not advertise resources');
                const current = await this.api.current(identity.catalogId);
                if (disposed || binding.closed || current.hash !== original.content.serverHash || current.connectionId !== binding.connectionId || current.threadId !== binding.threadId || current.conversationId !== binding.conversationId) throw new Error('Browser app catalog changed');
                const result = await binding.connection.request('resources/read', { uri: original.content.resourceUri });
                if (disposed || binding.closed) throw new Error('Browser app connection closed');
                return result;
            }, dispose: () => { disposed = true; }, observeOutcome: () => false, refreshPending: async () => {},
        };
    }
    async closeConversation(conversationId: string) { for (const [key, binding] of this.bindings) if (binding.conversationId === conversationId) await this.closeBinding(key, binding); }
    reset() { this.generation++; for (const [key, binding] of this.bindings) void this.closeBinding(key, binding); }
}
