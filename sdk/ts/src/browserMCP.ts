import type { Tool, ToolCall } from '@ag-ui/core';
import type { ClientTool, ClientToolResult } from './agui';

export interface BrowserMCPDescriptor {
    name: string;
    executionLocation: 'browser';
    transport: { type: 'chrome-extension'; extensionId: string; portName: string; requestTimeoutMs?: number };
    allowedTools: string[];
    outputArtifacts?: Record<string, { name?: string; namePath?: string; bytesPath?: string; mimeTypePath?: string; mimeType?: string; encoding?: 'base64' }>;
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
export type BrowserMCPCatalogStatus = Omit<BrowserMCPCatalog, 'tools' | 'resources'>;
export interface BrowserMCPHostOptions {
    transportFactory?: (descriptor: BrowserMCPDescriptor, binding: { conversationId: string; threadId: string; connectionId: string }) => BrowserMCPConnection;
    onApprovalRequired?: (server: string) => void | (() => void);
}
interface BrowserMCPAPI {
    descriptors(): Promise<BrowserMCPDescriptor[]>;
    register(input: { conversationId: string; threadId: string; server: string; connectionId: string; tools: unknown[]; resources?: boolean }): Promise<BrowserMCPCatalog>;
    revoke(id: string): Promise<unknown>;
    current(id: string): Promise<BrowserMCPCatalogStatus>;
}
type ChromePort = { postMessage(value: unknown): void; disconnect(): void; onMessage: { addListener(callback: (value: any) => void): void }; onDisconnect: { addListener(callback: () => void): void } };
type ChromeRuntime = { connect(extensionId: string, options: { name: string }): ChromePort; lastError?: unknown };

// Transport bounds precede backend overflow/artifact policy. Images must reach
// that policy intact; truncating encoded data would destroy the picture.
const maxToolResultBytes = 12 * 1024 * 1024;
const maxImageBytes = 8 * 1024 * 1024;
function imageDataBytes(data: unknown): number {
    const invalid = () => { throw new Error('Browser MCP image content is invalid'); };
    if (typeof data !== 'string' || !data.length || data.length % 4 || data.length > Math.ceil(maxImageBytes / 3) * 4) return invalid();
    const padding = data.endsWith('==') ? 2 : data.endsWith('=') ? 1 : 0;
    const bodyLength = data.length - padding;
    const sextet = (code: number): number => code >= 65 && code <= 90 ? code - 65 : code >= 97 && code <= 122 ? code - 71 : code >= 48 && code <= 57 ? code + 4 : code === 43 ? 62 : code === 47 ? 63 : -1;
    let last = 0;
    for (let index = 0; index < bodyLength; index++) {
        last = sextet(data.charCodeAt(index));
        if (last < 0) return invalid();
    }
    if (padding === 1 && (last & 3) || padding === 2 && (last & 15)) return invalid();
    const bytes = data.length / 4 * 3 - padding;
    if (bytes > maxImageBytes) return invalid();
    return bytes;
}

const setupMessages = {
    runtimeUnavailable: 'Chrome extension transport is unavailable on this page. Open the page in Chrome with the configured extension enabled.',
    invalidDescriptor: 'The browser MCP extension configuration is invalid.',
    metadataUnavailable: 'The workspace could not provide its browser MCP configuration.',
    initializeUnavailable: 'The extension connection failed during MCP initialization.',
    connectionHandshakeDeadline: 'The extension did not respond to the connection request. Check that the configured extension is enabled on this page.',
    approvalDeadlineExceeded: 'The browser connection approval deadline exceeded. Approve the pending page connection in the local application while the approval notice is visible, then try again.',
    extensionReceiverUnavailable: 'The configured Chrome extension could not be reached from this page. Check that it is installed and enabled in this browser and allows this page.',
    connectionClosedBeforeHandshake: 'The extension connection closed before responding to this page. Check the configured extension and local application connection.',
    connectionClosedBeforeApproval: 'The extension connection closed before page approval. Check the pending controller request in the local application.',
    connectionLost: 'The approved extension connection closed. Check the local application connection before trying again.',
    requestDeadlineExceeded: 'The approved extension did not answer the MCP request before its deadline. Check the local application connection before trying again.',
    connectionRequestUnavailable: 'Browser MCP connection unavailable before request dispatch.',
    requestUnavailable: 'The approved extension could not complete the MCP request. Check the local application connection before trying again.',
    catalogUnavailable: 'The extension did not provide a valid MCP tool catalog.',
    catalogRegistrationUnavailable: 'The server could not register this browser tool catalog.',
    senderMetadataUnavailable: 'Chrome did not provide the page identity required by the local connection.',
    senderURLInvalid: 'Chrome did not provide a valid page address for the local connection.',
    controllerPortNameInvalid: 'The configured extension connection name is not supported.',
    controllerOriginDenied: 'This page origin is not allowed by the extension controller.',
    controllerPolicyUnavailable: 'The extension has not received its local controller configuration. Reconnect the local application.',
    controllerPolicyDisabled: 'Browser controller access is disabled in the local application.',
    controllerPolicyInvalid: 'The local controller configuration is invalid. Review its configured page origins.',
    senderOriginMismatch: 'The page identity does not match its address.',
    senderFrameDenied: 'The local connection must originate from the top-level page.',
    senderTabInvalid: 'Chrome did not provide a valid tab identity.',
    senderDocumentInvalid: 'Chrome did not provide a valid document identity.',
    controllerCapacity: 'The extension has reached its connection limit. Close an unused connection and retry.',
    nativeChannelUnavailable: 'The extension is disconnected from the local application. Reconnect it before retrying.',
    resultTransferInvalid: 'The browser tool result failed its transfer checks. The original call will not be replayed.',
    resultTransferTimeout: 'The browser tool result transfer timed out. The original call will not be replayed.',
    resultTransferTooLarge: 'The browser tool result exceeds the bounded transfer size. The original call will not be replayed.',
} as const;
type SetupCode = keyof typeof setupMessages;
export class BrowserMCPSetupError extends Error {
    constructor(readonly code: SetupCode, readonly dispatchState?: 'notDispatched' | 'unknown') { super(setupMessages[code]); }
}
type BrowserMCPBindingCode = 'bindingClosed' | 'originChanged' | 'catalogMissing' | 'catalogExpired' | 'toolNameMismatch' | 'catalogIdentityMismatch' | 'connectionIdentityMismatch';
type BrowserMCPCloseCause = 'bindingChanged' | 'setupTransportClosed' | 'setupFailed' | 'conversationIdle' | 'explicitClose' | 'accountReset';
/** Coarse lifecycle feedback only; never accepts identities, tool data or errors. */
export function recordBrowserMCPLifecycle(event: 'settled' | 'close' | 'continuation' | 'aliases', detail: { phase?: 'idle' | 'running' | 'completed' | 'interrupted' | 'cancelled' | 'failed' | 'detached'; nativeIdentity?: boolean; browserTools?: boolean; retained?: boolean; pendingCount?: number; interruptCount?: number; exactCount?: number; canonicalCount?: number; slashCount?: number; dashCount?: number; cause?: BrowserMCPCloseCause; stage?: 'execute' | 'interrupts' | 'resume' | 'idle' | 'error'; failure?: 'missingPendingCall' | 'missingClientHandler' | 'interruptExpired' | 'argumentsInvalid' | 'catalogUnavailable' | 'executionFailed' }) {
    try {
        const name = `agently.browserMCP.lifecycle.${event}`;
        globalThis.performance?.clearMarks(name);
        globalThis.performance?.mark(name, { detail });
    } catch { /* Diagnostics must not change catalog or execution behavior. */ }
}
/** A closed local condition only: no catalog IDs, origins, arguments or errors. */
export class BrowserMCPBindingError extends Error {
    constructor(readonly code: BrowserMCPBindingCode) { super(`Original browser MCP catalog is unavailable (${code}).`); }
}
/** Only locally recognized codes may become UI diagnostics; never relay raw errors. */
export function browserMCPSetupMessage(error: unknown): string {
    const detail = error instanceof BrowserMCPSetupError && Object.hasOwn(setupMessages, error.code) ? setupMessages[error.code] : 'Check the local browser connection and try again.';
    return `Browser tools are not connected. ${detail} Your message was not sent.`;
}
function setupFailure(error: unknown, code: SetupCode): never {
    throw error instanceof BrowserMCPSetupError && error.code !== 'requestUnavailable' ? error : new BrowserMCPSetupError(code);
}

// Bounded User Timing diagnostics: durations only, never tool arguments,
// credentials, origins, conversation IDs, or catalog contents.
export function finishBrowserTiming(phase: 'setup' | 'metadata' | 'initialize' | 'catalog' | 'register' | 'tool.total' | 'tool.catalogCurrent' | 'tool.nativeRequest' | 'tool.resultProjection' | 'transport.queue' | 'transport.roundTrip' | 'resource.catalogCurrent' | 'resource.nativeRequest' | 'handoff.pendingToHandler' | 'handoff.terminalToHandler' | 'handoff.handlers' | 'handoff.continuation', started: number | undefined): void {
    if (started === undefined) return;
    try {
        const clock = globalThis.performance, name = `agently.browserMCP.${phase}`;
        clock.clearMeasures(name);
        clock.measure(name, { start: started, end: clock.now() });
    } catch { /* Diagnostics must not affect execution. */ }
}
async function measureBrowserSetup<T>(phase: Parameters<typeof finishBrowserTiming>[0], operation: () => Promise<T>): Promise<T> {
    const started = globalThis.performance?.now();
    try { return await operation(); }
    finally { finishBrowserTiming(phase, started); }
}

/** Generic extension MCP envelope adapter: public addressing only, no HTTP,
 * workspace cookie, OAuth token, native-host name or tool-specific code. */
export class ChromeExtensionMCPConnection implements BrowserMCPConnection {
    onclose?: () => void;
    private port!: ChromePort;
    private ready: Promise<void>;
    private rejectReady!: (error: Error) => void;
    private readyTimer?: ReturnType<typeof setTimeout>;
    private awaitingApproval = false;
    private approved = false;
    private dismissApproval?: () => void;
    private pending = new Map<string, { resolve(value: unknown): void; reject(error: Error): void; timer: ReturnType<typeof setTimeout>; maxResultBytes: number }>();
    private closed = false;
    private queue: Promise<unknown> = Promise.resolve();
    private queued = 0;
    private timeout: number;
    constructor(descriptor: BrowserMCPDescriptor, onApprovalRequired?: () => void | (() => void), runtime: ChromeRuntime | undefined = (globalThis as any).chrome?.runtime) {
        const config = descriptor.transport;
        if (config.type !== 'chrome-extension' || !/^[a-p]{32}$/.test(config.extensionId) || !/^[A-Za-z0-9._:-]{1,128}$/.test(config.portName)) throw new BrowserMCPSetupError('invalidDescriptor');
        if (!runtime?.connect) throw new BrowserMCPSetupError('runtimeUnavailable');
        this.timeout = config.requestTimeoutMs || 9000;
        if (!Number.isSafeInteger(this.timeout) || this.timeout < 100 || this.timeout > 9000) throw new Error('Invalid browser MCP deadline');
        this.ready = new Promise((resolve, reject) => {
            this.rejectReady = reject;
            this.readyTimer = setTimeout(() => {
                this.rejectReady(new BrowserMCPSetupError('connectionHandshakeDeadline'));
                this.close();
            }, this.timeout);
            this.port = runtime.connect(config.extensionId, { name: config.portName });
            this.port.onMessage.addListener(message => {
                if (this.closed) return;
                if (message?.type === 'setupRefused') {
                    this.rejectReady(typeof message.code === 'string' && Object.hasOwn(setupMessages, message.code)
                        ? new BrowserMCPSetupError(message.code as SetupCode) : new Error('Browser MCP setup refused'));
                    this.close();
                    return;
                }
                if (message?.type === 'approvalRequired') {
                    if (this.approved) return;
                    if (!this.awaitingApproval) {
                        this.awaitingApproval = true;
                        clearTimeout(this.readyTimer);
                        // Human interaction has a separate bounded wait. Tool
                        // calls keep their short deadline and cannot start yet.
                        this.readyTimer = setTimeout(() => {
                            this.rejectReady(new BrowserMCPSetupError('approvalDeadlineExceeded'));
                            this.close();
                        }, 120000);
                        const dismiss = onApprovalRequired?.();
                        if (typeof dismiss === 'function') this.dismissApproval = dismiss;
                    }
                    return;
                }
                if (message?.type === 'approved') { this.approved = true; clearTimeout(this.readyTimer); this.clearApproval(); resolve(); return; }
                if (message?.type !== 'result') return;
                const pending = this.pending.get(message.requestId); if (!pending) return;
                this.pending.delete(message.requestId); clearTimeout(pending.timer);
                if (['resultTransferInvalid', 'resultTransferTimeout', 'resultTransferTooLarge', 'resultTooLarge'].includes(message.error)) {
                    pending.reject(new BrowserMCPSetupError(message.error === 'resultTooLarge' ? 'resultTransferTooLarge' : message.error as SetupCode, 'unknown'));
                } else if (message.result && new TextEncoder().encode(JSON.stringify(message.result)).length <= pending.maxResultBytes) pending.resolve(message.result);
                else pending.reject(new BrowserMCPSetupError('requestUnavailable'));
            });
            this.port.onDisconnect.addListener(() => {
                // Consume Chrome's callback-local diagnostic, but recognize only
                // its fixed missing-receiver message. Never relay runtime text.
                const error = runtime.lastError;
                const missingReceiver = !!error && typeof error === 'object' &&
                    'message' in error && error.message === 'Could not establish connection. Receiving end does not exist.';
                this.finish(missingReceiver);
            });
        });
        this.ready.catch(() => undefined);
    }
    async request(method: 'initialize' | 'tools/list' | 'tools/call' | 'resources/read' | 'ping', params?: Record<string, unknown>): Promise<any> {
        if (this.closed || this.queued >= 16) throw new BrowserMCPSetupError('connectionRequestUnavailable', 'notDispatched');
        this.queued++;
        const queuedAt = globalThis.performance?.now();
        const run = this.queue.then(async () => {
            await this.ready;
            finishBrowserTiming('transport.queue', queuedAt);
            if (this.closed) throw new BrowserMCPSetupError('connectionLost', 'notDispatched');
            const requestId = crypto.randomUUID();
            const request = { type: 'request', requestId, operation: method, deadlineUnixMs: Date.now() + this.timeout, ...(params ? { params } : {}) };
            if (new TextEncoder().encode(JSON.stringify(request)).length > 65536) throw new Error('Browser MCP request exceeds transport budget');
            return measureBrowserSetup('transport.roundTrip', () => new Promise((resolve, reject) => {
                const timer = setTimeout(() => { this.pending.delete(requestId); reject(new BrowserMCPSetupError('requestDeadlineExceeded', 'unknown')); }, this.timeout);
                this.pending.set(requestId, { resolve, reject, timer, maxResultBytes: method === 'tools/call' ? maxToolResultBytes : 256 * 1024 });
                try { this.port.postMessage(request); } catch { clearTimeout(timer); this.pending.delete(requestId); reject(new Error('Browser MCP transport unavailable')); }
            }));
        });
        this.queue = run.catch(() => undefined);
        try { return await run; } finally { this.queued--; }
    }
    private finish(missingReceiver = false) {
        if (this.closed) return; this.closed = true;
        clearTimeout(this.readyTimer);
        this.clearApproval();
        const code: SetupCode = this.approved ? 'connectionLost' : this.awaitingApproval ? 'connectionClosedBeforeApproval' :
            missingReceiver ? 'extensionReceiverUnavailable' : 'connectionClosedBeforeHandshake';
        this.rejectReady(new BrowserMCPSetupError(code));
        for (const p of this.pending.values()) { clearTimeout(p.timer); p.reject(new BrowserMCPSetupError('connectionLost', 'unknown')); }
        this.pending.clear(); this.onclose?.();
    }
    private clearApproval() {
        const dismiss = this.dismissApproval;
        this.dismissApproval = undefined;
        try { dismiss?.(); } catch { /* presentation cleanup does not own transport authority */ }
    }
    close() { try { this.port?.disconnect(); } finally { this.finish(); } }
}
interface Binding {
    conversationId: string; threadId: string; config: string; connectionId: string; connection: BrowserMCPConnection;
    resources?: boolean; apps: Map<string, BrowserMCPAppActivity>; closed: boolean; transportLost?: boolean; revoked?: boolean; ready?: Promise<void>; origin: string; catalog?: BrowserMCPCatalog; tools: ClientTool[]; attempts: Map<string, { fingerprint: string; result?: ClientToolResult; pending?: Promise<ClientToolResult> }>;
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
    private setupAttempts = new Map<string, Set<{ cancelled: boolean }>>();
    async tools(conversationId: string, threadId: string): Promise<ClientTool[]> {
        const generation = this.generation, attempt = { cancelled: false };
        const attempts = this.setupAttempts.get(conversationId) ?? new Set<{ cancelled: boolean }>();
        attempts.add(attempt); this.setupAttempts.set(conversationId, attempts);
        try {
            for (let retry = 0; ; retry++) {
                try { return await measureBrowserSetup('setup', () => this.toolsAttempt(conversationId, threadId)); }
                catch (error) {
                    const transient = error instanceof BrowserMCPSetupError &&
                        ['connectionLost', 'connectionClosedBeforeHandshake', 'requestDeadlineExceeded'].includes(error.code);
                    if (retry >= 1 || !transient || attempt.cancelled || generation !== this.generation) throw error;
                    // Only discovery is retried. tools/call never enters this path.
                }
            }
        } finally {
            attempts.delete(attempt);
            if (!attempts.size && this.setupAttempts.get(conversationId) === attempts) this.setupAttempts.delete(conversationId);
        }
    }
    private async toolsAttempt(conversationId: string, threadId: string): Promise<ClientTool[]> {
        const generation = this.generation;
        const descriptors = await measureBrowserSetup('metadata', () => this.api.descriptors()).catch(error => setupFailure(error, 'metadataUnavailable'));
        if (generation !== this.generation) throw new Error('Browser MCP identity changed');
        const wanted = new Set(descriptors.filter(item => item.executionLocation === 'browser').map(item => item.name));
        for (const [key, binding] of this.bindings) if (binding.conversationId === conversationId && !wanted.has(JSON.parse(binding.config).name)) await this.closeBinding(key, binding);
        const result: ClientTool[] = [];
        for (const descriptor of descriptors) {
            if (descriptor.executionLocation !== 'browser') continue;
            const key = `${conversationId}\0${descriptor.name}`, config = JSON.stringify(descriptor);
            let binding = this.bindings.get(key);
            if (binding && (binding.closed || binding.transportLost || binding.threadId !== threadId || binding.origin !== (globalThis.location?.origin ?? '') || binding.config !== config || binding.catalog && Date.parse(binding.catalog.expiresAt) <= Date.now())) { await this.closeBinding(key, binding); binding = undefined; }
            if (!binding) {
                const connectionId = crypto.randomUUID();
                const connection = this.options.transportFactory?.(descriptor, { conversationId, threadId, connectionId }) ?? new ChromeExtensionMCPConnection(descriptor, () => this.options.onApprovalRequired?.(descriptor.name));
                binding = { conversationId, threadId, config, connectionId, connection, origin: globalThis.location?.origin ?? '', closed: false, apps: new Map(), tools: [], attempts: new Map() };
                this.bindings.set(key, binding);
                const active = binding;
                connection.onclose = () => {
                    if (!active.catalog) { void this.closeBinding(key, active, 'setupTransportClosed'); return; }
                    // A lost socket is not catalog revocation. Retain only the
                    // original bounded call outcomes so its waiting turn can
                    // receive terminal failures under the still-current catalog.
                    // Explicit close/reset still revokes and clears everything.
                    active.transportLost = true;
                    active.apps.clear();
                    for (const listener of this.appListeners) listener();
                };
                active.ready = (async () => { try {
                    const initialized = await measureBrowserSetup('initialize', () => connection.request('initialize', {
                        protocolVersion: '2025-11-25',
                        clientInfo: { name: 'agently-core-ui-sdk', version: '0.1.0' },
                        capabilities: { extensions: { 'io.modelcontextprotocol/ui': { mimeTypes: ['text/html;profile=mcp-app'] } } },
                    })).catch(error => setupFailure(error, 'initializeUnavailable'));
                    active.resources = !!initialized?.capabilities?.resources;
                    const catalog = await measureBrowserSetup('catalog', () => connection.request('tools/list')).catch(error => setupFailure(error, 'catalogUnavailable'));
                    if (active.closed || generation !== this.generation || !Array.isArray(catalog?.tools) || catalog.tools.length > 64) throw new BrowserMCPSetupError('catalogUnavailable');
                    const allowed = (name: string) => descriptor.allowedTools.some(pattern => new RegExp('^' + pattern.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*').replace(/\?/g, '.') + '$').test(name));
                    const tools = catalog.tools.filter((tool: any) => typeof tool?.name === 'string' && allowed(tool.name)).map((tool: any) => ({ name: tool.name, description: tool.description || '', inputSchema: tool.inputSchema, ...(tool._meta ? { _meta: tool._meta } : {}) }));
                    active.catalog = await measureBrowserSetup('register', () => this.api.register({ conversationId, threadId, server: descriptor.name, connectionId, tools, ...(active.resources ? { resources: true } : {}) })).catch(error => setupFailure(error, 'catalogRegistrationUnavailable'));
                    if (active.closed || generation !== this.generation || active.catalog.connectionId !== connectionId || active.catalog.conversationId !== conversationId || active.catalog.threadId !== threadId) throw new Error('Browser MCP catalog binding changed');
                    active.tools = active.catalog.tools.filter(tool => toolAudience(tool, 'model')).map(tool => ({ tool, execute: (args, call) => this.call(active, tool, args, call) }));
                } catch (error) { await this.closeBinding(key, active, 'setupFailed'); throw error; } })();
            }
            if (binding.ready) await binding.ready;
            result.push(...binding.tools);
        }
        return result;
    }
    private async call(binding: Binding, tool: Tool, args: unknown, call: ToolCall): Promise<ClientToolResult> {
        return measureBrowserSetup('tool.total', () => this.callBound(binding, tool, args, call));
    }
    private async callBound(binding: Binding, tool: Tool, args: unknown, call: ToolCall): Promise<ClientToolResult> {
        const metadata = tool.metadata as any;
        const identity = metadata?.browserMCP;
        const catalog = this.requireOriginalCatalog(binding);
        if (call.function.name !== tool.name) throw new BrowserMCPBindingError('toolNameMismatch');
        if (identity?.catalogId !== catalog.id) throw new BrowserMCPBindingError('catalogIdentityMismatch');
        if (identity?.connectionId !== binding.connectionId) throw new BrowserMCPBindingError('connectionIdentityMismatch');
        const current = await measureBrowserSetup('tool.catalogCurrent', () => this.api.current(catalog.id));
        if (binding.closed || current.hash !== catalog.hash || current.connectionId !== binding.connectionId || current.threadId !== binding.threadId) throw new Error('Browser MCP catalog was revoked or changed');
        if (!Number.isFinite(Date.parse(current.expiresAt)) || Date.parse(current.expiresAt) <= Date.now()) throw new BrowserMCPBindingError('catalogExpired');
        catalog.expiresAt = current.expiresAt;
        const boundArgs = JSON.parse(call.function.arguments);
        if (JSON.stringify(boundArgs) !== JSON.stringify(args)) throw new Error('Browser MCP arguments differ from the pending call');
        const fingerprint = JSON.stringify([binding.conversationId, binding.threadId, binding.connectionId, tool.name, call.function.arguments]);
        const previous = binding.attempts.get(call.id);
        if (previous) {
            if (previous.fingerprint !== fingerprint) throw new Error('Browser MCP call is conflicting or uncertain; it will not be replayed');
            if (previous.result) return structuredClone(previous.result);
            if (previous.pending) return structuredClone(await previous.pending);
            throw new Error('Browser MCP call is conflicting or uncertain; it will not be replayed');
        }
        if (binding.attempts.size >= 256) throw new Error('Browser MCP call quota exhausted');
        const attempt: { fingerprint: string; pending?: Promise<ClientToolResult> } = { fingerprint };
        binding.attempts.set(call.id, attempt);
        const completion = (async (): Promise<ClientToolResult> => {
            const terminalFailure = (code: 'browserTransportTimeout' | 'browserTransportUnavailable' | 'resultTransferInvalid' | 'resultTransferTimeout' | 'resultTransferTooLarge', dispatchState: 'unknown' | 'notDispatched'): ClientToolResult => {
                // Do not publish after an explicit identity/ownership boundary.
                this.requireOriginalCatalog(binding);
                const answer: ClientToolResult = {
                    content: JSON.stringify({ code, dispatchState, outcome: dispatchState === 'unknown' ? 'unknown' : 'notDispatched' }),
                    error: dispatchState === 'unknown' ? 'Browser tool transport failed; outcome is unknown. This call will not be replayed.' : 'Browser tool connection is unavailable; this call was not dispatched.',
                    metadata: structuredClone(tool.metadata),
                };
                binding.attempts.set(call.id, { fingerprint, result: structuredClone(answer) });
                return answer;
            };
            if (binding.transportLost) return terminalFailure('browserTransportUnavailable', 'notDispatched');
            let result: any;
            try { result = await measureBrowserSetup('tool.nativeRequest', () => binding.connection.request('tools/call', { name: identity.tool, arguments: boundArgs })); }
            catch (error) {
                if (error instanceof BrowserMCPSetupError && (error.code === 'resultTransferInvalid' || error.code === 'resultTransferTimeout' || error.code === 'resultTransferTooLarge')) return terminalFailure(error.code, 'unknown');
                return terminalFailure(error instanceof BrowserMCPSetupError && error.code === 'requestDeadlineExceeded' ? 'browserTransportTimeout' : 'browserTransportUnavailable', error instanceof BrowserMCPSetupError && error.dispatchState === 'notDispatched' ? 'notDispatched' : 'unknown');
            }
            const projectionStarted = globalThis.performance?.now();
            try {
                if (binding.closed) throw new Error('Browser MCP result unavailable for the original conversation');
                if (!result || !Array.isArray(result.content)) return terminalFailure('browserTransportUnavailable', 'unknown');
                if (result.resultType !== undefined && result.resultType !== 'complete') return terminalFailure('resultTransferInvalid', 'unknown');
                // MCP _meta is host/UI-only and must not become model content.
                const images = result.content.filter((item: any) => item?.type === 'image');
                let totalImageBytes = 0;
                for (const item of images) {
                    if (!/^(image\/(png|jpeg|webp|gif))$/.test(item.mimeType)) return terminalFailure('resultTransferInvalid', 'unknown');
                    try { totalImageBytes += imageDataBytes(item.data); }
                    catch { return terminalFailure('resultTransferInvalid', 'unknown'); }
                    if (totalImageBytes > maxImageBytes) return terminalFailure('resultTransferTooLarge', 'unknown');
                }
                let content: ClientToolResult['content'];
                const artifactPolicy = (JSON.parse(binding.config) as BrowserMCPDescriptor).outputArtifacts?.[identity.tool];
                if (artifactPolicy !== undefined) {
                    // Preserve the canonical MCP shape for backend JSON-pointer
                    // extraction. Backend policy, not browser metadata, owns it.
                    const cleanBlock = (value: any): any => Array.isArray(value) ? value.map(cleanBlock) : value && typeof value === 'object'
                        ? Object.fromEntries(Object.entries(value).filter(([key]) => key !== '_meta').map(([key, item]) => [key, cleanBlock(item)])) : value;
                    content = JSON.stringify({ content: result.content.map(cleanBlock), ...(result.structuredContent !== undefined ? { structuredContent: result.structuredContent } : {}), ...(result.isError !== undefined ? { isError: result.isError } : {}) });
                } else if (images.length) {
                    const parts: any[] = [];
                    if (result.structuredContent !== undefined) parts.push({ type: 'text', text: JSON.stringify(result.structuredContent) });
                    for (const item of result.content) {
                        if (item?.type === 'text' && typeof item.text === 'string') {
                            // Canonical structured metadata replaces its duplicate text
                            // block, while image order remains unchanged.
                            if (result.structuredContent === undefined) parts.push({ type: 'text', text: item.text });
                        } else if (item?.type === 'image') {
                            parts.push({ type: 'image', source: { type: 'data', value: item.data, mimeType: item.mimeType } });
                        }
                    }
                    content = parts;
                } else content = JSON.stringify(result.structuredContent ?? result.content);
                const relayContent = typeof content === 'string' ? content : JSON.stringify(content);
                if (new TextEncoder().encode(relayContent).length > maxToolResultBytes) return terminalFailure('resultTransferTooLarge', 'unknown');
                const answer: ClientToolResult = { content, ...(result.isError ? { error: 'Browser MCP tool returned an error' } : {}), metadata: structuredClone(tool.metadata) };
                binding.attempts.set(call.id, { fingerprint, result: structuredClone(answer) });
                const resourceUri = metadata?.ui?.resourceUri;
                if (typeof resourceUri === 'string' && resourceUri.startsWith('ui://')) {
                    const appInstanceId = crypto.randomUUID();
                    binding.apps.set(call.id, { mountKey: appInstanceId, content: { resourceUri, result: structuredClone(result), toolInput: boundArgs,
                        serverId: catalog.server, serverHash: catalog.hash,
                        _agentlyApp: { appInstanceId, threadId: binding.threadId, nativeTurnId: '' },
                        _browserMCP: { catalogId: catalog.id, connectionId: binding.connectionId, callId: call.id, conversationId: binding.conversationId } } });
                    for (const listener of this.appListeners) listener();
                }
                return answer;
            } finally { finishBrowserTiming('tool.resultProjection', projectionStarted); }
        })();
        attempt.pending = completion;
        // Validation failures remain reserved and non-replayable. Only a
        // currently in-flight duplicate shares the same pending completion.
        void completion.then(() => { attempt.pending = undefined; }, () => { attempt.pending = undefined; });
        return completion;
    }
    private requireOriginalCatalog(binding: Binding): BrowserMCPCatalog {
        if (binding.closed) throw new BrowserMCPBindingError('bindingClosed');
        if (binding.origin !== (globalThis.location?.origin ?? '')) throw new BrowserMCPBindingError('originChanged');
        if (!binding.catalog) throw new BrowserMCPBindingError('catalogMissing');
        if (Date.parse(binding.catalog.expiresAt) <= Date.now()) throw new BrowserMCPBindingError('catalogExpired');
        return binding.catalog;
    }
    private async closeBinding(key: string, binding: Binding, cause: BrowserMCPCloseCause = 'bindingChanged') {
        const wasClosed = binding.closed; binding.closed = true; binding.attempts.clear(); binding.apps.clear();
        if (!wasClosed) recordBrowserMCPLifecycle('close', { cause });
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
                if (disposed || binding.closed || binding.transportLost || binding.origin !== (globalThis.location?.origin ?? '')) throw new Error('Browser app connection unavailable');
                // Local discovery cannot grant guest execution. Browser Apps
                // advertise no serverTools until an approval-aware route exists.
                if (method !== 'resources/read' || params.uri !== original.content.resourceUri) throw new Error('Browser app method outside resource binding');
                if (!binding.resources) throw new Error('Browser MCP server does not advertise resources');
                const current = await measureBrowserSetup('resource.catalogCurrent', () => this.api.current(identity.catalogId));
                if (disposed || binding.closed || binding.transportLost || current.hash !== original.content.serverHash || current.connectionId !== binding.connectionId || current.threadId !== binding.threadId || current.conversationId !== binding.conversationId) throw new Error('Browser app catalog changed');
                if (!Number.isFinite(Date.parse(current.expiresAt)) || Date.parse(current.expiresAt) <= Date.now()) throw new BrowserMCPBindingError('catalogExpired');
                if (binding.catalog) binding.catalog.expiresAt = current.expiresAt;
                const result = await measureBrowserSetup('resource.nativeRequest', () => binding.connection.request('resources/read', { uri: original.content.resourceUri }));
                if (disposed || binding.closed || binding.transportLost) throw new Error('Browser app connection closed');
                return result;
            }, dispose: () => { disposed = true; }, observeOutcome: () => false, refreshPending: async () => {},
        };
    }
    async closeConversation(conversationId: string, cause: 'explicitClose' | 'conversationIdle' = 'explicitClose') { for (const attempt of this.setupAttempts.get(conversationId) ?? []) attempt.cancelled = true; for (const [key, binding] of this.bindings) if (binding.conversationId === conversationId) await this.closeBinding(key, binding, cause); }
    reset() { this.generation++; for (const [key, binding] of this.bindings) void this.closeBinding(key, binding, 'accountReset'); }
}
