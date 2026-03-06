/**
 * TypeScript types mirroring agently-core/sdk/types.go and related Go models.
 *
 * These types correspond 1:1 to the JSON wire format used by the agently-core
 * HTTP API. Field names use camelCase as serialised by Go's json tags.
 */

// ─── Pagination ────────────────────────────────────────────────────────────────

export type Direction = 'before' | 'after' | 'latest';

export interface PageInput {
    limit?: number;
    cursor?: string;
    direction?: Direction;
}

export interface PageOutput {
    cursor?: string;
    hasMore?: boolean;
    total?: number;
}

// ─── Conversation ──────────────────────────────────────────────────────────────

export interface Conversation {
    id: string;
    agentId?: string;
    title?: string;
    summary?: string;
    stage?: string;
    visibility?: string;
    shareable?: boolean;
    conversationParentId?: string;
    createdAt: string;
    lastActivity?: string;
    createdByUserId?: string;
    promptTokens?: number;
    completionTokens?: number;
    totalTokens?: number;
    cost?: number;
}

export interface ConversationPage {
    data: Conversation[];
    page?: PageOutput;
}

export interface CreateConversationInput {
    agentId?: string;
    title?: string;
    metadata?: Record<string, any>;
}

export interface UpdateConversationVisibilityInput {
    visibility?: string;
    shareable?: boolean;
}

export interface ListConversationsInput {
    agentId?: string;
    query?: string;
    status?: string;
    page?: PageInput;
}

// ─── Turn / Transcript ─────────────────────────────────────────────────────────

export interface Turn {
    id: string;
    conversationId: string;
    status: string;
    elapsedInSec?: number;
    stage?: string;
    queueSeq?: number;
    agentIdUsed?: string;
    modelOverride?: string;
    modelOverrideProvider?: string;
    startedByMessageId?: string;
    errorMessage?: string;
    runId?: string;
    createdAt: string;
    message: Message[];
}

export interface TranscriptOutput {
    turns: Turn[];
}

export interface GetTranscriptInput {
    conversationId: string;
    since?: string;
    includeModelCalls?: boolean;
    includeToolCalls?: boolean;
}

// ─── Message ───────────────────────────────────────────────────────────────────

export interface Message {
    id: string;
    conversationId: string;
    turnId?: string;
    role: string;
    type: string;
    content?: string;
    rawContent?: string;
    status?: string;
    interim: number;
    iteration?: number;
    preamble?: string;
    phase?: string;
    mode?: string;
    sequence?: number;
    archived?: number;
    createdAt: string;
    updatedAt?: string;
    createdByUserId?: string;
    elicitationId?: string;
    elicitationPayloadId?: string;
    parentMessageId?: string;
    linkedConversationId?: string;
    attachmentPayloadId?: string;
    toolName?: string;
    supersededBy?: string;
    contextSummary?: string;
    summary?: string;
    embeddingIndex?: string;
    tags?: string;
    toolMessage?: ToolMessageView[];
    userElicitationData?: UserElicitationDataView;
    linkedConversation?: LinkedConversationView;
    attachment?: AttachmentView[];
    modelCall?: ModelCallView;
}

export interface ToolMessageView {
    id: string;
    parentMessageId?: string;
    createdAt: string;
    type: string;
    content?: string;
    toolName?: string;
    iteration?: number;
    toolCall?: ToolCallView;
}

export interface ToolCallView {
    id: string;
    toolName: string;
    iteration?: number;
    attempt?: number;
    status: string;
    elapsedMs?: number;
    cost?: number;
    errorMessage?: string;
    requestPayloadId?: string;
    responsePayloadId?: string;
    traceId?: string;
    createdAt: string;
    completedAt?: string;
}

export interface ModelCallView {
    id: string;
    messageId?: string;
    model?: string;
    provider?: string;
    iteration?: number;
    status: string;
    promptTokens?: number;
    completionTokens?: number;
    totalTokens?: number;
    cost?: number;
    latencyMs?: number;
    finishReason?: string;
    createdAt: string;
    completedAt?: string;
}

export interface UserElicitationDataView {
    id: string;
    messageId?: string;
    inlineBody?: string;
}

export interface LinkedConversationView {
    id: string;
    title?: string;
    agentId?: string;
}

export interface AttachmentView {
    id: string;
    parentMessageId?: string;
    name?: string;
    mimeType?: string;
    content?: string;
    uri?: string;
}

export interface MessagePage {
    data: Message[];
    page?: PageOutput;
}

export interface GetMessagesInput {
    conversationId: string;
    turnId?: string;
    roles?: string[];
    types?: string[];
    page?: PageInput;
}

// ─── Streaming ─────────────────────────────────────────────────────────────────

export type SSEEventType = 'chunk' | 'tool' | 'done' | 'error' | 'control';

export interface SSEEvent {
    id?: string;
    streamId?: string;
    type: SSEEventType;
    op?: string;
    patch?: Record<string, any>;
    content?: string;
    toolName?: string;
    arguments?: Record<string, any>;
    error?: string;
    createdAt?: string;
}

export interface StreamEventsInput {
    conversationId: string;
}

// ─── Query ─────────────────────────────────────────────────────────────────────

export interface QueryInput {
    conversationId?: string;
    query: string;
    agentId?: string;
    model?: string;
    tools?: string[];
    toolBundles?: string[];
    autoSelectTools?: boolean;
    context?: Record<string, any>;
    attachments?: QueryAttachment[];
    reasoningEffort?: string;
    autoSummarize?: boolean;
    disableChains?: boolean;
    allowedChains?: string[];
    toolCallExposure?: string;
}

export interface QueryAttachment {
    name: string;
    uri: string;
    size?: number;
    mime?: string;
    stagingFolder?: string;
}

export interface QueryOutput {
    conversationId: string;
    content: string;
    model?: string;
    messageId?: string;
    elicitation?: any;
    plan?: any;
    usage?: UsageInfo;
    warnings?: string[];
}

export interface UsageInfo {
    promptTokens?: number;
    completionTokens?: number;
    totalTokens?: number;
    cost?: number;
}

// ─── Steer / Queue ─────────────────────────────────────────────────────────────

export interface SteerTurnInput {
    content: string;
    role?: string;
}

export interface SteerTurnOutput {
    messageId: string;
    turnId?: string;
    status?: string;
    canceledTurnId?: string;
}

export interface MoveQueuedTurnInput {
    direction: 'up' | 'down';
}

export interface EditQueuedTurnInput {
    content: string;
}

// ─── Elicitations ──────────────────────────────────────────────────────────────

export interface PendingElicitation {
    conversationId: string;
    elicitationId: string;
    messageId: string;
    status: string;
    role: string;
    type: string;
    createdAt: string;
    content?: string;
}

export interface ResolveElicitationInput {
    action: string;
    payload?: Record<string, any>;
}

// ─── Tool Approvals ────────────────────────────────────────────────────────────

export interface PendingToolApproval {
    id: string;
    userId: string;
    conversationId?: string;
    turnId?: string;
    messageId?: string;
    toolName: string;
    title?: string;
    arguments?: Record<string, any>;
    metadata?: Record<string, any>;
    status: string;
    decision?: string;
    createdAt: string;
    updatedAt?: string;
    errorMessage?: string;
}

export interface DecideToolApprovalInput {
    action: 'approve' | 'reject';
    userId?: string;
    reason?: string;
    payload?: Record<string, any>;
}

export interface DecideToolApprovalOutput {
    status: string;
}

// ─── Files ─────────────────────────────────────────────────────────────────────

export interface FileEntry {
    id: string;
    name: string;
    contentType: string;
    size: number;
}

export interface UploadFileInput {
    conversationId: string;
    name: string;
    contentType: string;
    data: Blob | File;
}

export interface UploadFileOutput {
    id: string;
    uri: string;
}

// ─── Workspace Resources ───────────────────────────────────────────────────────

export interface ResourceRef {
    kind: string;
    name: string;
}

export interface Resource {
    kind: string;
    name: string;
    data: string;
}

// ─── Run ───────────────────────────────────────────────────────────────────────

export interface RunView {
    id: string;
    turnId?: string;
    messageId?: string;
    model?: string;
    provider?: string;
    status?: string;
    createdAt?: string;
}
