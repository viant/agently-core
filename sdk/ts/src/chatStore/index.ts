/**
 * agently-core-ui-sdk/chatStore — one canonical client-state for chat feeds.
 *
 * See ui-improvement.md for the full contract. Public API:
 *   types:          Client* state types, Canonical* transcript shape mirrors
 *   identity:       renderKey allocator, per-entity-kind match helpers
 *   lifecycle:      backend status → client lifecycle mapping
 *   reducer:        applyLocalSubmit, applyEvent, applyTranscript
 *   projector:      projectConversation → RenderRow[]
 */

export * from './types';
export * from './identity';
export * from './lifecycle';
export {
    applyEvent,
    applyLocalSubmit,
    applyTranscript,
    getFieldProvenance,
    isEffectiveValue,
    newConversationState,
    writeField,
    setFieldProvenance,
} from './reducer';
export {
    describeHeader,
    projectConversation,
    projectTurn,
    roundHasContent,
    toneForLifecycle,
    type ElicitationRenderView,
    type HeaderState,
    type HeaderTone,
    type IterationRenderRow,
    type LifecycleEntryRenderView,
    type LinkedConversationRenderView,
    type ModelStepRenderView,
    type RenderRow,
    type RenderRowKind,
    type RoundRenderView,
    type ToolCallRenderView,
    type UserRenderRow,
} from './projector';
