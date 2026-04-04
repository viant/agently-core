/**
 * TypeScript types mirroring agently-core/sdk/types.go and related Go models.
 *
 * These types correspond 1:1 to the JSON wire format used by the agently-core
 * HTTP API. Field names use camelCase as serialised by Go's json tags.
 */

export type JSONPrimitive = string | number | boolean | null;
export type JSONValue = JSONPrimitive | JSONObject | JSONArray;
export interface JSONObject {
    [key: string]: JSONValue;
}
export interface JSONArray extends Array<JSONValue> {}

export interface ConversationStateLike {
    stage?: string;
    Stage?: string;
    status?: string;
    Status?: string;
}

// ─── Pagination ────────────────────────────────────────────────────────────────

export type Direction = 'before' | 'after' | 'latest';

export interface PageInput {
    limit?: number;
    cursor?: string;
    direction?: Direction;
}

export interface PageOutput {
    cursor?: string;
    prevCursor?: string;
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
    metadata?: JSONObject;
    parentConversationId?: string;
    parentTurnId?: string;
}

export interface UpdateConversationInput {
    title?: string;
    visibility?: string;
    shareable?: boolean;
}

export interface ListConversationsInput {
    agentId?: string;
    excludeScheduled?: boolean;
    query?: string;
    status?: string;
    page?: PageInput;
}

// ─── Turn / Transcript ─────────────────────────────────────────────────────────

export interface Turn {
    id: string;
    turnId?: string;
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
    execution?: {
        pages?: Partial<ExecutionPage>[];
    };
    executionGroups?: ExecutionGroup[];
    executionGroupsTotal?: number;
    executionGroupsOffset?: number;
    executionGroupsLimit?: number;
}

export interface TranscriptOutput {
    turns: Turn[];
}

export interface GetTranscriptInput {
    conversationId: string;
    since?: string;
    includeModelCalls?: boolean;
    includeToolCalls?: boolean;
    /** Include resolved tool feed data in the response. */
    includeFeeds?: boolean;
}

export interface QuerySelector {
    limit?: number;
    offset?: number;
    orderBy?: string;
}

export interface GetTranscriptOptions {
    selectors?: Record<string, QuerySelector>;
    executionGroupSelector?: QuerySelector;
    executionGroupLimit?: number;
    executionGroupOffset?: number;
}

export interface ExecutionPage {
    pageId: string;
    assistantMessageId: string;
    parentMessageId: string;
    turnId?: string;
    iteration?: number;
    preamble?: string;
    content?: string;
    finalResponse: boolean;
    status?: string;
    modelSteps: ModelStepState[];
    toolSteps: ToolStepState[];
    toolCallsPlanned?: PlannedToolCall[];
    preambleMessageId?: string;
    finalAssistantMessageId?: string;
}

export type LiveExecutionGroup = Partial<ExecutionPage> & {
    sequence?: number;
    errorMessage?: string;
};

export type LiveExecutionGroupsById = Record<string, LiveExecutionGroup>;

export interface ModelStepState {
    modelCallId: string;
    assistantMessageId?: string;
    provider?: string;
    model?: string;
    status?: string;
    errorMessage?: string;
    requestPayloadId?: string;
    responsePayloadId?: string;
    providerRequestPayloadId?: string;
    providerResponsePayloadId?: string;
    streamPayloadId?: string;
    startedAt?: string;
    completedAt?: string;
}

export interface ToolStepState {
    toolCallId: string;
    toolMessageId?: string;
    toolName: string;
    status?: string;
    errorMessage?: string;
    requestPayloadId?: string;
    responsePayloadId?: string;
    linkedConversationId?: string;
    linkedConversationAgentId?: string;
    linkedConversationTitle?: string;
    startedAt?: string;
    completedAt?: string;
}

/** @deprecated Use ExecutionPage instead */
export type ExecutionGroup = ExecutionPage;

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

export type SSEEventType =
    // Stream deltas
    | 'text_delta'
    | 'reasoning_delta'
    | 'tool_call_delta'
    | 'error'
    // Turn lifecycle
    | 'turn_started'
    | 'turn_completed'
    | 'turn_failed'
    | 'turn_canceled'
    // Model lifecycle
    | 'model_started'
    | 'model_completed'
    // Assistant content (aggregated)
    | 'assistant_preamble'
    | 'assistant_final'
    // Tool call lifecycle
    | 'tool_call_started'
    | 'tool_call_completed'
    // Metadata
    | 'item_completed'
    | 'usage'
    // Elicitation
    | 'elicitation_requested'
    | 'elicitation_resolved'
    // Linked conversation
    | 'linked_conversation_attached'
    // Tool feed lifecycle
    | 'tool_feed_active'
    | 'tool_feed_inactive'
    // Control (patch-based)
    | 'control';

export interface EventModel {
    provider?: string;
    model?: string;
    kind?: string;
}

export interface PlannedToolCall {
    toolCallId?: string;
    toolName?: string;
}

export interface SSEEvent {
    id?: string;
    streamId?: string;
    conversationId?: string;
    turnId?: string;
    messageId?: string;
    eventSeq?: number;
    mode?: string;
    agentIdUsed?: string;
    agentName?: string;
    assistantMessageId?: string;
    parentMessageId?: string;
    requestId?: string;
    responseId?: string;
    toolCallId?: string;
    toolMessageId?: string;
    requestPayloadId?: string;
    responsePayloadId?: string;
    providerRequestPayloadId?: string;
    providerResponsePayloadId?: string;
    streamPayloadId?: string;
    linkedConversationId?: string;
    linkedConversationAgentId?: string;
    linkedConversationTitle?: string;
    type: SSEEventType;
    op?: string;
    patch?: JSONObject;
    content?: string;
    preamble?: string;
    toolName?: string;
    arguments?: JSONObject;
    error?: string;
    status?: string;
    iteration?: number;
    pageIndex?: number;
    pageCount?: number;
    latestPage?: boolean;
    finalResponse?: boolean;
    model?: EventModel;
    toolCallsPlanned?: PlannedToolCall[];
    createdAt?: string;
    completedAt?: string;
    startedAt?: string;
    userMessageId?: string;
    modelCallId?: string;
    provider?: string;
    modelName?: string;
    elicitationId?: string;
    elicitationData?: JSONObject;
    callbackUrl?: string;
    responsePayload?: JSONObject;
    // Tool feed fields
    feedId?: string;
    feedTitle?: string;
    feedItemCount?: number;
    feedData?: JSONValue;
}

export type ExecutionEvent = SSEEvent;

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
    context?: JSONObject;
    attachments?: QueryAttachment[];
    reasoningEffort?: string;
    elicitationMode?: string;
    userId?: string;
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
    elicitation?: JSONObject;
    plan?: JSONObject;
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
    payload?: JSONObject;
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
    arguments?: JSONObject;
    metadata?: JSONObject;
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
    payload?: JSONObject;
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

// ─── Scheduler ──────────────────────────────────────────────────────────────────

export interface Schedule {
    id: string;
    name: string;
    agentRef: string;
    createdByUserId?: string;
    visibility?: string;
    enabled: boolean;
    scheduleType: string;
    cronExpr?: string;
    intervalSeconds?: number;
    timezone?: string;
    taskPrompt?: string;
    taskPromptUri?: string;
    userCredUrl?: string;
    nextRunAt?: string;
    lastRunAt?: string;
    createdAt: string;
    updatedAt: string;
}

export interface ScheduleListOutput {
    schedules: Schedule[];
}

// ─── Workspace Metadata ────────────────────────────────────────────────────────

export interface AgentInfo {
    id: string;
    name?: string;
    description?: string;
    modelRef?: string;
    starterTasks?: StarterTask[];
}

export interface ModelInfo {
    id: string;
    name?: string;
    provider?: string;
}

export interface StarterTask {
    id?: string;
    title?: string;
    prompt?: string;
    agentId?: string;
}

export interface WorkspaceCapabilities {
    agentAutoSelection?: boolean;
    modelAutoSelection?: boolean;
    toolAutoSelection?: boolean;
    compactConversation?: boolean;
    pruneConversation?: boolean;
    anonymousSession?: boolean;
    messageCursor?: boolean;
    structuredElicitation?: boolean;
    turnStartedEvent?: boolean;
}

export interface WorkspaceDefaults {
    agent?: string;
    model?: string;
    embedder?: string;
    autoSelectTools?: boolean;
}

export interface WorkspaceMetadata {
    defaultAgent?: string;
    defaultModel?: string;
    defaultEmbedder?: string;
    defaults?: WorkspaceDefaults;
    capabilities?: WorkspaceCapabilities;
    agents?: string[];
    models?: string[];
    agentInfos?: AgentInfo[];
    modelInfos?: ModelInfo[];
    version?: string;
}

// ─── Payload ──────────────────────────────────────────────────────────────────

export interface GetPayloadOptions {
    /** Return raw binary content with original Content-Type (default false). */
    raw?: boolean;
    /** Return only metadata without inline body (default false). */
    meta?: boolean;
    /** Include InlineBody in response (default true, ignored if meta=true). */
    inline?: boolean;
}

export interface PayloadView {
    id: string;
    tenantId?: string;
    kind?: string;
    subtype?: string;
    mimeType?: string;
    sizeBytes?: number;
    digest?: string;
    storage?: string;
    inlineBody?: string;
    uri?: string;
    compression?: string;
    encryptionKmsKeyId?: string;
    redactionPolicyVersion?: string;
    redacted?: number;
    createdAt?: string;
    schemaRef?: string;
}

// ─── Linked Conversations ─────────────────────────────────────────────────────

export interface ListLinkedConversationsInput {
    parentConversationId: string;
    parentTurnId?: string;
    page?: PageInput;
}

export interface LinkedConversationEntry {
    conversationId: string;
    parentConversationId: string;
    parentTurnId: string;
    agentId?: string;
    title?: string;
    status?: string;
    response?: string;
    createdAt: string;
    updatedAt?: string;
}

export interface LinkedConversationPage {
    data: LinkedConversationEntry[];
    page?: PageOutput;
}

// ─── Auth ─────────────────────────────────────────────────────────────────────

export interface AuthProvider {
    name: string;
    label?: string;
    type: string;
    /** Present for local providers. */
    defaultUsername?: string;
    /** Present for oidc/spa providers. */
    clientID?: string;
    discoveryURL?: string;
    redirectURI?: string;
    scopes?: string[];
    /** Present for bff providers. */
    mode?: string;
}

export interface AuthUser {
    subject?: string;
    username?: string;
    email?: string;
    displayName?: string;
    provider?: string;
    preferences?: JSONObject;
}

export interface LocalLoginInput {
    username: string;
}

export interface LocalLoginOutput {
    sessionId: string;
    username: string;
    provider?: string;
}

export interface OAuthInitiateOutput {
    authURL: string;
    state: string;
    provider?: string;
    delegated?: boolean;
}

export interface OAuthCallbackInput {
    code: string;
    state: string;
}

export interface OAuthCallbackOutput {
    status: string;
    username?: string;
    provider?: string;
}

export interface OAuthConfigOutput {
    mode?: string;
    configURL?: string;
    clientId?: string;
    discoveryUrl?: string;
    redirectUri?: string;
    scopes?: string[];
}

export interface CreateSessionInput {
    username?: string;
    accessToken?: string;
    idToken?: string;
    refreshToken?: string;
}

export interface CreateSessionOutput {
    sessionId: string;
    username?: string;
}

export interface OOBLoginInput {
    accessToken: string;
    idToken?: string;
    refreshToken?: string;
    username?: string;
}

export interface IDPDelegateOutput {
    mode: string;
    idpLogin: string;
    provider?: string;
    authURL: string;
    state: string;
    expiresIn?: number;
}

// ─── Tool Feeds ────────────────────────────────────────────────────────────────

export interface FeedSpec {
    id: string;
    title: string;
    match: { service: string; method: string };
}

export interface ActiveFeed {
    feedId: string;
    title: string;
    itemCount: number;
    conversationId?: string;
    turnId?: string;
    updatedAt: number;
}
