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
} from './types';

// Streaming reconciliation
export {
    newMessageBuffer, applyEvent, reconcileMessages, reconcileFromTranscript,
} from './reconcile';
export type { MessageBuffer } from './reconcile';

// Message interpretation helpers
export {
    isPreamble, isFinalResponse, isUserMessage, isToolMessage,
    isSystemMessage, isArchived, isSummary, isSummarized,
    toolName, toolStatus, toolElapsedMs, toolCallId,
    messageIteration, messagePreamble,
    groupByIteration, messageUIType,
} from './interpret';
