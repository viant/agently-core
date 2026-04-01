/**
 * agently-core TypeScript SDK — public API.
 *
 * Usage:
 *   import { AgentlyClient } from '@agently-core/sdk';
 *   const client = new AgentlyClient({ baseURL: 'http://localhost:8585/v1' });
 */

// Client
export { AgentlyClient } from './client';
export type { ClientOptions, TokenProvider } from './client';

// Errors
export { HttpError } from './errors';

// Types
export type {
    // Pagination
    Direction, PageInput, PageOutput,
    // Conversation
    Conversation, ConversationPage, CreateConversationInput,
    ListConversationsInput, UpdateConversationInput,
    // Turn / Transcript
    Turn, TranscriptOutput, GetTranscriptInput, GetTranscriptOptions, QuerySelector,
    // Execution pages (canonical)
    ExecutionPage, ModelStepState, ToolStepState,
    // Message
    Message, MessagePage, GetMessagesInput,
    ToolMessageView, ToolCallView, ModelCallView,
    UserElicitationDataView, LinkedConversationView, AttachmentView,
    // Streaming
    SSEEvent, SSEEventType, StreamEventsInput,
    // Query
    QueryInput, QueryOutput, QueryAttachment, UsageInfo,
    // Steer / Queue
    SteerTurnInput, SteerTurnOutput,
    MoveQueuedTurnInput, EditQueuedTurnInput,
    // Elicitations
    PendingElicitation, ResolveElicitationInput,
    // Tool Approvals
    PendingToolApproval, DecideToolApprovalInput, DecideToolApprovalOutput,
    // Files
    FileEntry, UploadFileOutput,
    // Resources
    Resource, ResourceRef,
    // Run
    RunView,
    // Scheduler
    Schedule, ScheduleListOutput,
    // Workspace Metadata
    WorkspaceMetadata, AgentInfo, ModelInfo, StarterTask,
    WorkspaceCapabilities, WorkspaceDefaults,
    // Payload
    PayloadView, GetPayloadOptions,
    // Linked Conversations
    ListLinkedConversationsInput, LinkedConversationEntry, LinkedConversationPage,
    // Auth
    AuthProvider, AuthUser, LocalLoginInput, LocalLoginOutput,
    OAuthInitiateOutput, OAuthCallbackInput, OAuthCallbackOutput,
    OAuthConfigOutput, CreateSessionInput, CreateSessionOutput,
    OOBLoginInput, IDPDelegateOutput,
    // Tool Feeds
    FeedSpec, ActiveFeed,
} from './types';

// Elicitation tracking
export { ElicitationTracker } from './elicitation';
export type { PendingElicitation as TrackedElicitation, ElicitationListener } from './elicitation';

// Feed tracking
export { FeedTracker } from './feedTracker';
export type { FeedListener } from './feedTracker';

// Linked conversation preview helpers
export {
    summarizeLinkedConversationTranscript,
    reduceLinkedConversationPreviewEvent,
} from './linkedConversations';
export type {
    LinkedConversationPreviewGroup,
    LinkedConversationPreviewSummary,
} from './linkedConversations';

// Streaming reconciliation
export {
    newMessageBuffer, applyEvent, reconcileMessages, reconcileFromTranscript,
} from './reconcile';
export type { MessageBuffer } from './reconcile';

// High-level stream tracker
export { ConversationStreamTracker } from './conversationStream';
export type { ConversationStreamSnapshot, CanonicalConversationSnapshot } from './conversationStream';

// Canonical execution group helpers
export {
    normalizeExecutionPageSize,
    plannedToolCalls as plannedExecutionToolCalls,
    isPresentableExecutionGroup,
    selectExecutionPages,
    selectExecutionSteps,
    findExecutionStepById,
    findExecutionStepByPayloadId,
    applyExecutionStreamEventToGroups,
    mergeLatestTranscriptAndLiveExecutionGroups,
    describeExecutionTimelineEvent,
} from './executionGroups';
export type { ExecutionStepLike } from './executionGroups';

// Stream identity helpers
export {
    resolveEventConversationId,
    resolveEventTurnId,
    resolveEventMessageId,
} from './streamIdentity';

// Rich content rendering (pluggable fence registry, markdown, charts, tables)
export {
    parseFences, languageHint,
    findNextPipeTableBlock, looksLikePipeTable, parsePipeTable,
    parseChartSpecFromFence, normalizeChartSpec, buildChartSeries,
    escapeHTML, escapeHTMLAttr, resolveHref,
    inlineMarkdown, renderMarkdownCellHTML, renderMarkdownBlock,
    FenceRendererRegistry, getDefaultRegistry,
    registerFenceRenderer, registerFenceClassifier,
} from './richContent';
export type {
    FencePart, TableBlock, ParsedTable,
    ChartSpec, ChartDef, ChartAxis, NormalizedChart, ChartSeries,
    BuiltinRendererType, FenceClassification, FenceClassifier,
} from './richContent';

// Message interpretation helpers
export {
    isPreamble, isFinalResponse, isUserMessage, isToolMessage,
    isSystemMessage, isArchived, isSummary, isSummarized,
    toolName, toolStatus, toolElapsedMs, toolCallId,
    messageIteration, messagePreamble,
    groupByIteration, messageUIType,
} from './interpret';
