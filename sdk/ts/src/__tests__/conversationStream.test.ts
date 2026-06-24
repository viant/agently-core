import { describe, expect, it } from 'vitest';

import { buildEffectiveLiveAssistantRows, buildEffectiveLiveRows, ConversationStreamTracker, filterExplicitLiveRowsAgainstTracker, hasLiveAssistantRowForTurn, latestEffectiveLiveAssistantRow, latestLiveAssistantRowForTurn, latestLiveAssistantRowForTurnWithTransientState, overlayLiveAssistantTransientState, projectLiveAssistantRows, projectTrackerToTurns, selectLiveAssistantRowsForTurn } from '../conversationStream';
import type { Message, SSEEvent, Turn } from '../types';

describe('ConversationStreamTracker', () => {
    it('applies message, feed, and elicitation events through one facade', () => {
        const tracker = new ConversationStreamTracker();

        const delta = tracker.applyEvent({
            id: 'msg-1',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            type: 'text_delta',
            content: 'Hello',
        } as SSEEvent);
        expect(delta).toEqual({ id: 'msg-1', content: 'Hello', final: false });
        expect(tracker.activeTurnId).toBe('turn-1');
        expect(tracker.conversationId).toBe('conv-1');

        tracker.applyEvent({
            type: 'tool_feed_active',
            feedId: 'plan',
            feedTitle: 'Plan',
            feedItemCount: 2,
            conversationId: 'conv-1',
            turnId: 'turn-1',
        } as SSEEvent);
        expect(tracker.feeds).toHaveLength(1);
        expect(tracker.feeds[0]).toMatchObject({ feedId: 'plan', conversationId: 'conv-1', turnId: 'turn-1' });

        tracker.applyEvent({
            type: 'goal.updated',
            conversationId: 'conv-1',
            turnId: 'turn-1',
        } as SSEEvent);
        expect(tracker.feeds.find((feed) => feed.feedId === 'goal')).toMatchObject({
            feedId: 'goal',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            itemCount: 1,
        });

        tracker.applyEvent({
            type: 'elicitation_requested',
            elicitationId: 'elic-1',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            content: 'Need input',
            elicitationData: { requestedSchema: { type: 'object' } },
        } as SSEEvent);
        expect(tracker.pendingElicitation).toMatchObject({
            elicitationId: 'elic-1',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            message: 'Need input',
        });
        expect(tracker.state).toMatchObject({
            conversationId: 'conv-1',
            activeTurnId: 'turn-1',
        });
        expect(tracker.compositeState).toMatchObject({
            conversationId: 'conv-1',
            activeTurnId: 'turn-1',
        });
        expect(tracker.canonicalState).toMatchObject({
            conversationId: 'conv-1',
            activeTurnId: 'turn-1',
            bufferedMessages: [
                expect.objectContaining({
                    id: 'msg-1',
                    content: 'Hello',
                }),
            ],
            liveExecutionGroupsById: {
                'msg-1': expect.objectContaining({
                    assistantMessageId: 'msg-1',
                }),
            },
        });

        tracker.applyEvent({
            type: 'goal.cleared',
            conversationId: 'conv-1',
            turnId: 'turn-1',
        } as SSEEvent);
        expect(tracker.feeds.find((feed) => feed.feedId === 'goal')).toMatchObject({
            itemCount: 0,
        });

        tracker.applyEvent({
            type: 'goal.updated',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            patch: {
                goal: {
                    id: 'goal-1',
                    objective: 'Ship release',
                    status: 'active',
                },
            },
        } as SSEEvent);
        tracker.applyEvent({
            type: 'goal.controller_scheduled',
            conversationId: 'conv-1',
            patch: {
                mode: 'wakeup',
                reason: 'delay active goal continuation',
                preview: 'Continue later',
                wakeAt: '2026-06-08T19:30:00Z',
            },
        } as SSEEvent);
        expect(tracker.feeds.find((feed) => feed.feedId === 'goal')).toMatchObject({
            data: {
                goal: expect.objectContaining({
                    id: 'goal-1',
                    objective: 'Ship release',
                }),
                controllerSchedule: expect.objectContaining({
                    mode: 'wakeup',
                    reason: 'delay active goal continuation',
                    preview: 'Continue later',
                    wakeAt: '2026-06-08T19:30:00Z',
                }),
            },
        });
    });

    it('reconciles transcript and server messages through one facade', () => {
        const tracker = new ConversationStreamTracker();
        tracker.applyEvent({
            id: 'msg-1',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            type: 'text_delta',
            content: 'Hello',
        } as SSEEvent);

        tracker.applyTranscript([{
            id: 'turn-1',
            conversationId: 'conv-1',
            status: 'completed',
            createdAt: '2026-01-01T00:00:00Z',
            message: [{
                id: 'msg-1',
                conversationId: 'conv-1',
                role: 'assistant',
                type: 'text',
                content: 'Hello world',
                interim: 0,
                createdAt: '2026-01-01T00:00:01Z',
            }],
        } as Turn]);

        const merged = tracker.reconcile([{
            id: 'msg-1',
            conversationId: 'conv-1',
            role: 'assistant',
            type: 'text',
            content: 'Hello world',
            interim: 0,
            createdAt: '2026-01-01T00:00:01Z',
        } as Message]);

        expect(merged).toHaveLength(1);
        expect(merged[0].content).toBe('Hello world');
        expect(tracker.snapshot().bufferedMessages).toHaveLength(1);
        expect(tracker.snapshotCanonical()).toMatchObject({
            conversationId: 'conv-1',
            activeTurnId: 'turn-1',
            bufferedMessages: [
                expect.objectContaining({
                    id: 'msg-1',
                    content: 'Hello world',
                }),
            ],
            liveExecutionGroupsById: {},
        });
        expect(tracker.snapshotComposite()).toMatchObject({
            conversationId: 'conv-1',
        });
        tracker.reset();
        expect(tracker.state.bufferedMessages).toHaveLength(0);
        expect(tracker.canonicalState).toMatchObject({
            conversationId: '',
            activeTurnId: null,
            feeds: [],
            pendingElicitation: null,
            bufferedMessages: [],
            liveExecutionGroupsById: {},
        });
    });

    it('planner SSE events update turn planner state', () => {
        const tracker = new ConversationStreamTracker('conv-1');

        tracker.applyEvent({
            type: 'turn_started',
            conversationId: 'conv-1',
            turnId: 'turn-1',
        } as SSEEvent);
        tracker.applyEvent({
            type: 'planner.selected',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            plannerTrigger: 'exploratory_strategy',
            plannerStaticProfile: 'repo_analysis',
        } as SSEEvent);
        tracker.applyEvent({
            type: 'planner.output',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            plannerStrategyFamily: 'troubleshoot',
            plannerAttempt: 1,
            plannerOutputPayloadId: 'planner-output:conv-1:turn-1',
        } as SSEEvent);
        tracker.applyEvent({
            type: 'planner.validated',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            plannerAttempt: 1,
            plannerValidated: true,
        } as SSEEvent);

        expect(tracker.snapshot().plannerByTurnId?.['turn-1']).toMatchObject({
            status: 'validated',
            trigger: 'exploratory_strategy',
            staticProfile: 'repo_analysis',
            strategyFamily: 'troubleshoot',
            attempt: 1,
            outputPayloadId: 'planner-output:conv-1:turn-1',
            validated: true,
        });
    });

    it('transcript hydrate populates planner state for past turns', () => {
        const tracker = new ConversationStreamTracker('conv-1');

        tracker.applyTranscript([{
            id: 'turn-past',
            conversationId: 'conv-1',
            status: 'completed',
            createdAt: '2026-01-01T00:00:00Z',
            message: [],
            planner: {
                status: 'failed',
                trigger: 'low_confidence',
                strategyFamily: 'troubleshoot',
                attempt: 2,
                secondPolicy: 'clarify',
                outputPayloadId: 'planner-output:conv-1:turn-past',
                validated: false,
            },
        } as Turn]);

        expect(tracker.snapshot().plannerByTurnId?.['turn-past']).toMatchObject({
            status: 'failed',
            trigger: 'low_confidence',
            strategyFamily: 'troubleshoot',
            attempt: 2,
            secondPolicy: 'clarify',
            outputPayloadId: 'planner-output:conv-1:turn-past',
            validated: false,
        });
    });

    it('transcript does not overwrite active turn planner state owned by SSE', () => {
        const tracker = new ConversationStreamTracker('conv-1');

        tracker.applyEvent({
            type: 'turn_started',
            conversationId: 'conv-1',
            turnId: 'turn-live',
        } as SSEEvent);
        tracker.applyEvent({
            type: 'planner.failed',
            conversationId: 'conv-1',
            turnId: 'turn-live',
            plannerTrigger: 'exploratory_strategy',
            plannerAttempt: 2,
            plannerSecondPolicy: 'block',
        } as SSEEvent);

        tracker.applyTranscript([{
            id: 'turn-live',
            conversationId: 'conv-1',
            status: 'running',
            createdAt: '2026-01-01T00:00:00Z',
            message: [],
            planner: {
                status: 'selected',
                trigger: 'low_confidence',
                attempt: 1,
            },
        } as Turn]);

        expect(tracker.snapshot().plannerByTurnId?.['turn-live']).toMatchObject({
            status: 'failed',
            trigger: 'exploratory_strategy',
            attempt: 2,
            secondPolicy: 'block',
        });
    });

    it('transcript does not overwrite active turn message and execution owned by SSE', () => {
        const tracker = new ConversationStreamTracker('conv-1');

        tracker.applyEvent({
            type: 'turn_started',
            conversationId: 'conv-1',
            turnId: 'turn-live',
        } as SSEEvent);
        tracker.applyEvent({
            type: 'assistant',
            conversationId: 'conv-1',
            turnId: 'turn-live',
            messageId: 'assistant-live',
            content: 'SSE active response',
            patch: { role: 'assistant' },
        } as SSEEvent);
        tracker.applyEvent({
            type: 'tool_call_started',
            conversationId: 'conv-1',
            turnId: 'turn-live',
            assistantMessageId: 'assistant-live',
            toolCallId: 'tool-live',
            toolName: 'system/exec',
            status: 'running',
        } as SSEEvent);

        tracker.applyTranscript([
            {
                id: 'turn-history',
                conversationId: 'conv-1',
                status: 'completed',
                createdAt: '2026-01-01T00:00:00Z',
                assistant: {
                    final: {
                        messageId: 'assistant-history',
                        content: 'Historical response',
                    },
                },
            },
            {
                id: 'turn-live',
                conversationId: 'conv-1',
                status: 'running',
                createdAt: '2026-01-01T00:00:01Z',
                execution: {
                    pages: [{
                        pageId: 'stale-page',
                        assistantMessageId: 'assistant-live',
                        turnId: 'turn-live',
                        status: 'completed',
                        content: 'stale transcript execution',
                    }],
                },
                assistant: {
                    final: {
                        messageId: 'assistant-live',
                        content: 'stale transcript response',
                    },
                },
                message: [{
                    id: 'assistant-live',
                    conversationId: 'conv-1',
                    role: 'assistant',
                    type: 'text',
                    content: 'stale transcript response',
                    interim: 0,
                    createdAt: '2026-01-01T00:00:02Z',
                }],
            },
        ] as Turn[]);

        expect(tracker.activeTurnId).toBe('turn-live');
        expect(tracker.snapshot().bufferedMessages).toEqual(
            expect.arrayContaining([
                expect.objectContaining({
                    id: 'assistant-live',
                    turnId: 'turn-live',
                    content: 'SSE active response',
                }),
                expect.objectContaining({
                    id: 'assistant-history',
                    turnId: 'turn-history',
                    content: 'Historical response',
                }),
            ]),
        );
        expect(tracker.snapshot().bufferedMessages).not.toEqual(
            expect.arrayContaining([
                expect.objectContaining({
                    id: 'assistant-live',
                    content: 'stale transcript response',
                }),
            ]),
        );
        expect(tracker.snapshot().liveExecutionGroupsById['assistant-live']).toMatchObject({
            assistantMessageId: 'assistant-live',
            turnId: 'turn-live',
            status: 'running',
            toolSteps: [expect.objectContaining({ toolCallId: 'tool-live' })],
        });
        expect(tracker.snapshot().liveExecutionGroupsById['assistant-live']).not.toMatchObject({
            content: 'stale transcript execution',
        });
    });

    it('projects startedByMessageId from turn_started user identity', () => {
        const tracker = new ConversationStreamTracker('conv-1');
        tracker.applyEvent({
            type: 'turn_started',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            userMessageId: 'user-1',
        } as SSEEvent);

        const turns = projectTrackerToTurns(tracker.canonicalState, 'conv-1');
        expect(turns).toEqual([
            expect.objectContaining({
                turnId: 'turn-1',
                startedByMessageId: 'user-1',
                user: expect.objectContaining({
                    messageId: 'user-1',
                }),
            }),
        ]);
    });

    it('projects tracker canonical state into live assistant rows', () => {
        const tracker = new ConversationStreamTracker('conv-1');
        tracker.applyEvent({
            type: 'model_started',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            messageId: 'msg-1',
            assistantMessageId: 'msg-1',
            status: 'running',
            model: { provider: 'openai', model: 'gpt-5.4' },
        } as SSEEvent);
        tracker.applyEvent({
            type: 'narration',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            messageId: 'msg-1',
            assistantMessageId: 'msg-1',
            content: 'Calling updatePlan.',
            status: 'running',
        } as SSEEvent);

        const rows = projectLiveAssistantRows(tracker.canonicalState, 'conv-1');
        expect(rows).toEqual([
            expect.objectContaining({
                id: 'msg-1',
                conversationId: 'conv-1',
                turnId: 'turn-1',
                content: 'Calling updatePlan.',
                narration: 'Calling updatePlan.',
                interim: 1,
                executionGroups: [
                    expect.objectContaining({
                        assistantMessageId: 'msg-1',
                        turnId: 'turn-1',
                        narration: 'Calling updatePlan.',
                    }),
                ],
            }),
        ]);
        expect(hasLiveAssistantRowForTurn(tracker.canonicalState, 'conv-1', 'turn-1')).toBe(true);
        expect(latestLiveAssistantRowForTurn(tracker.canonicalState, 'conv-1', 'turn-1')).toMatchObject({
            id: 'msg-1',
            turnId: 'turn-1',
            narration: 'Calling updatePlan.',
        });
    });

    it('projects bootstrap and first main round as separate execution pages when pageId is explicit', () => {
        const tracker = new ConversationStreamTracker('conv-1');

        tracker.applyEvent({
            type: 'tool_call_started',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            pageId: 'turn-1:bootstrap',
            toolCallId: 'bootstrap-1',
            toolName: 'llm/agents:list',
            mode: 'systemContext',
            iteration: 0,
            status: 'running',
        } as SSEEvent);

        tracker.applyEvent({
            type: 'model_started',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            pageId: 'msg-main-1',
            messageId: 'msg-main-1',
            assistantMessageId: 'msg-main-1',
            iteration: 1,
            status: 'running',
            model: { provider: 'openai', model: 'gpt-5.4' },
        } as SSEEvent);

        const turns = projectTrackerToTurns(tracker.canonicalState, 'conv-1');
        expect(turns).toHaveLength(1);
        expect(turns[0]?.execution?.pages).toEqual(
            expect.arrayContaining([
                expect.objectContaining({
                    pageId: 'turn-1:bootstrap',
                    assistantMessageId: 'turn-1:bootstrap',
                    phase: 'bootstrap',
                    executionRole: 'bootstrap',
                    toolSteps: [expect.objectContaining({ toolName: 'llm/agents:list' })],
                }),
                expect.objectContaining({
                    pageId: 'msg-main-1',
                    assistantMessageId: 'msg-main-1',
                    iteration: 1,
                    modelSteps: [expect.objectContaining({ provider: 'openai', model: 'gpt-5.4' })],
                }),
            ]),
        );
    });

    it('orders projected live assistant rows before selecting the latest row for a turn', () => {
        const tracker = new ConversationStreamTracker('conv-1');

        tracker.applyEvent({
            type: 'narration',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            messageId: 'msg-late',
            assistantMessageId: 'msg-late',
            content: 'Later page',
            createdAt: '2026-01-01T00:00:03Z',
            iteration: 2,
            status: 'running',
        } as SSEEvent);
        tracker.applyEvent({
            type: 'narration',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            messageId: 'msg-early',
            assistantMessageId: 'msg-early',
            content: 'Earlier page',
            createdAt: '2026-01-01T00:00:01Z',
            iteration: 1,
            status: 'completed',
        } as SSEEvent);

        const rows = projectLiveAssistantRows(tracker.canonicalState, 'conv-1');
        expect(rows.map((row) => row.id)).toEqual(['msg-early', 'msg-late']);
        expect(latestLiveAssistantRowForTurn(tracker.canonicalState, 'conv-1', 'turn-1')).toMatchObject({
            id: 'msg-late',
            content: 'Later page',
        });
    });

    it('selects tracker-backed live assistant rows for a single turn', () => {
        const tracker = new ConversationStreamTracker('conv-1');
        tracker.applyEvent({
            type: 'narration',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            messageId: 'msg-1',
            assistantMessageId: 'msg-1',
            content: 'one',
            status: 'running',
        } as SSEEvent);
        tracker.applyEvent({
            type: 'narration',
            conversationId: 'conv-1',
            turnId: 'turn-2',
            messageId: 'msg-2',
            assistantMessageId: 'msg-2',
            content: 'two',
            status: 'running',
        } as SSEEvent);

        expect(selectLiveAssistantRowsForTurn(tracker.canonicalState, 'conv-1', 'turn-1').map((row) => row.id)).toEqual(['msg-1']);
    });

    it('uses a deterministic createdAt fallback for projected live assistant rows', () => {
        const tracker = new ConversationStreamTracker('conv-1');

        tracker.applyEvent({
            type: 'model_started',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            messageId: 'msg-1',
            assistantMessageId: 'msg-1',
            status: 'running',
        } as SSEEvent);

        const first = projectLiveAssistantRows(tracker.canonicalState, 'conv-1');
        const second = projectLiveAssistantRows(tracker.canonicalState, 'conv-1');

        expect(first[0]?.createdAt).toBeTruthy();
        expect(second[0]?.createdAt).toBe(first[0]?.createdAt);
    });

    it('projects tracker snapshots into canonical turns with execution, linked conversations, and elicitation', () => {
        const tracker = new ConversationStreamTracker('conv-1');
        tracker.applyEvent({
            type: 'text_delta',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            id: 'msg-1',
            assistantMessageId: 'msg-1',
            content: 'Hello',
        } as SSEEvent);
        tracker.applyEvent({
            type: 'tool_call_started',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            assistantMessageId: 'msg-1',
            toolCallId: 'call-1',
            toolMessageId: 'tool-msg-1',
            toolName: 'llm/agents/run',
            status: 'running',
        } as SSEEvent);
        tracker.applyEvent({
            type: 'linked_conversation_attached',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            assistantMessageId: 'msg-1',
            toolCallId: 'call-1',
            linkedConversationId: 'child-1',
            linkedConversationAgentId: 'critic',
            linkedConversationTitle: 'Critic Review',
        } as SSEEvent);
        tracker.applyEvent({
            type: 'elicitation_requested',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            elicitationId: 'elic-1',
            content: 'Need input',
            callbackUrl: '/resolve',
            elicitationData: { requestedSchema: { type: 'object' } },
        } as SSEEvent);

        const turns = projectTrackerToTurns(tracker.canonicalState, 'conv-1');
        expect(turns).toEqual([
            expect.objectContaining({
                turnId: 'turn-1',
                conversationId: 'conv-1',
                status: 'waiting_for_user',
                execution: {
                    pages: [
                        expect.objectContaining({
                            assistantMessageId: 'msg-1',
                            turnId: 'turn-1',
                        }),
                    ],
                },
                linkedConversations: [
                    expect.objectContaining({
                        conversationId: 'child-1',
                        agentId: 'critic',
                        title: 'Critic Review',
                    }),
                ],
                elicitation: expect.objectContaining({
                    elicitationId: 'elic-1',
                    message: 'Need input',
                    callbackUrl: '/resolve',
                }),
            }),
        ]);
    });

    it('sorts projected turns deterministically by createdAt, then sequence, then turnId, and marks final rows completed when status is absent', () => {
        const tracker = new ConversationStreamTracker('conv-1');
        tracker.applyEvent({
            type: 'assistant',
            conversationId: 'conv-1',
            turnId: 'turn-b',
            messageId: 'msg-b',
            assistantMessageId: 'msg-b',
            content: 'Second',
            eventSeq: 2,
            patch: { role: 'assistant' },
        } as SSEEvent);
        tracker.applyEvent({
            type: 'assistant',
            conversationId: 'conv-1',
            turnId: 'turn-a',
            messageId: 'msg-a',
            assistantMessageId: 'msg-a',
            content: 'First',
            eventSeq: 1,
            patch: { role: 'assistant' },
        } as SSEEvent);

        const turns = projectTrackerToTurns(tracker.canonicalState, 'conv-1');
        expect(turns.map((turn) => turn.turnId)).toEqual(['turn-a', 'turn-b']);
        expect(turns[0]).toMatchObject({ status: 'completed' });
        expect(turns[1]).toMatchObject({ status: 'completed' });
    });

    it('overlays transient live assistant state onto projected tracker rows', () => {
        const tracker = new ConversationStreamTracker('conv-1');
        tracker.applyEvent({
            type: 'narration',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            messageId: 'msg-1',
            assistantMessageId: 'msg-1',
            content: 'Calling updatePlan.',
            status: 'running',
        } as SSEEvent);

        const projected = projectLiveAssistantRows(tracker.canonicalState, 'conv-1');
        const overlaid = overlayLiveAssistantTransientState(projected, [{
            id: 'msg-1',
            role: 'assistant',
            isStreaming: true,
            _streamContent: 'Calling updatePlan. Then streaming...'
        }]);

        expect(overlaid[0]).toMatchObject({
            id: 'msg-1',
            content: 'Calling updatePlan.',
            isStreaming: true,
            _streamContent: 'Calling updatePlan. Then streaming...'
        });
    });

    it('filters redundant explicit live assistant rows once tracker rows cover them', () => {
        const tracker = new ConversationStreamTracker('conv-1');
        tracker.applyEvent({
            type: 'narration',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            messageId: 'msg-1',
            assistantMessageId: 'msg-1',
            content: 'Calling updatePlan.',
            status: 'running',
        } as SSEEvent);
        const projected = projectLiveAssistantRows(tracker.canonicalState, 'conv-1');
        const filtered = filterExplicitLiveRowsAgainstTracker(projected, [
            { id: 'msg-1', role: 'assistant', turnId: 'turn-1', content: 'stale duplicate' },
            { id: 'assistant-turn-2', role: 'assistant', turnId: 'turn-2', content: 'keep me' },
            { id: 'user-1', role: 'user', turnId: 'turn-1', content: 'user row' },
            { id: 'stream-1', _type: 'stream', role: 'assistant', turnId: 'turn-1', content: 'stream row' },
        ]);

        expect(filtered).toEqual([
            expect.objectContaining({ id: 'assistant-turn-2', turnId: 'turn-2' }),
            expect.objectContaining({ id: 'user-1', role: 'user' }),
        ]);
    });

    it('builds effective live assistant rows from tracker canonical rows plus remaining explicit live rows', () => {
        const tracker = new ConversationStreamTracker('conv-1');
        tracker.applyEvent({
            type: 'narration',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            messageId: 'msg-1',
            assistantMessageId: 'msg-1',
            content: 'Calling updatePlan.',
            status: 'running',
        } as SSEEvent);

        const effective = buildEffectiveLiveAssistantRows(tracker.canonicalState, [
            {
                id: 'msg-1',
                role: 'assistant',
                turnId: 'turn-1',
                isStreaming: true,
                _streamContent: 'Calling updatePlan. Then streaming...',
            },
            {
                id: 'assistant-turn-2',
                role: 'assistant',
                turnId: 'turn-2',
                content: 'Second live turn',
            },
            {
                id: 'stream-1',
                _type: 'stream',
                role: 'assistant',
                turnId: 'turn-1',
                content: 'stream row',
            },
        ], 'conv-1');

        expect(effective.map((row) => String(row?.id || ''))).toEqual(['assistant-turn-2', 'msg-1']);
        expect(effective[0]).toMatchObject({
            id: 'assistant-turn-2',
            turnId: 'turn-2',
            content: 'Second live turn',
        });
        expect(effective[1]).toMatchObject({
            id: 'msg-1',
            isStreaming: true,
            _streamContent: 'Calling updatePlan. Then streaming...',
        });
    });

    it('builds effective live rows including preserved stream placeholders', () => {
        const tracker = new ConversationStreamTracker('conv-1');
        tracker.applyEvent({
            type: 'narration',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            messageId: 'msg-1',
            assistantMessageId: 'msg-1',
            content: 'Calling updatePlan.',
            status: 'running',
        } as SSEEvent);

        const effective = buildEffectiveLiveRows(tracker.canonicalState, [
            {
                id: 'msg-1',
                role: 'assistant',
                turnId: 'turn-1',
                isStreaming: true,
                _streamContent: 'Calling updatePlan. Then streaming...',
            },
            {
                id: 'stream-1',
                _type: 'stream',
                role: 'assistant',
                turnId: 'turn-1',
                content: 'stream row',
            },
        ], 'conv-1');

        expect(effective).toEqual([
            expect.objectContaining({ id: 'msg-1', isStreaming: true }),
            expect.objectContaining({ id: 'stream-1', _type: 'stream' }),
        ]);
    });

    it('does not apply same-turn transient overlay when multiple explicit assistant rows exist without an exact id match', () => {
        const tracker = new ConversationStreamTracker('conv-1');
        tracker.applyEvent({
            type: 'narration',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            messageId: 'msg-tracker',
            assistantMessageId: 'msg-tracker',
            content: 'Tracker canonical row',
            status: 'running',
            createdAt: '2026-01-01T00:00:02Z',
        } as SSEEvent);

        const row = latestLiveAssistantRowForTurnWithTransientState(
            tracker.canonicalState,
            [
                {
                    id: 'assistant-older',
                    role: 'assistant',
                    turnId: 'turn-1',
                    createdAt: '2026-01-01T00:00:01Z',
                    isStreaming: true,
                    _streamContent: 'older transient stream',
                },
                {
                    id: 'assistant-newer',
                    role: 'assistant',
                    turnId: 'turn-1',
                    createdAt: '2026-01-01T00:00:03Z',
                    isStreaming: true,
                    _streamContent: 'newer transient stream',
                },
            ],
            'conv-1',
            'turn-1',
        );

        expect(row).toMatchObject({
            id: 'msg-tracker',
            content: 'Tracker canonical row',
        });
        expect(row?._streamContent).toBeUndefined();
        expect(row?.isStreaming).toBeUndefined();
    });

    it('falls back to the latest explicit live assistant row when tracker has no row for the turn', () => {
        const row = latestEffectiveLiveAssistantRow(
            null,
            [
                {
                    id: 'assistant-older',
                    role: 'assistant',
                    turnId: 'turn-1',
                    createdAt: '2026-01-01T00:00:01Z',
                    content: 'older',
                },
                {
                    id: 'assistant-newer',
                    role: 'assistant',
                    turnId: 'turn-1',
                    createdAt: '2026-01-01T00:00:03Z',
                    content: 'newer',
                },
            ],
            'conv-1',
            'turn-1',
        );

        expect(row).toMatchObject({
            id: 'assistant-newer',
            content: 'newer',
        });
    });

    it('prefers a standalone assistant message row over a later lifecycle row for the same turn', () => {
        const row = latestEffectiveLiveAssistantRow(
            null,
            [
                {
                    id: 'assistant-final',
                    role: 'assistant',
                    turnId: 'turn-1',
                    createdAt: '2026-01-01T00:00:02Z',
                    content: 'Your report summary is now open for entity 2656980.',
                    interim: 0,
                    _bubbleSource: 'message_add',
                    executionGroups: [],
                },
                {
                    id: 'turn:turn-1',
                    role: 'assistant',
                    turnId: 'turn-1',
                    createdAt: '2026-01-01T00:00:03Z',
                    content: '',
                    interim: 1,
                    executionGroups: [
                        { pageId: 'turn:turn-1:lifecycle', turnId: 'turn-1', status: 'running' } as any,
                    ],
                },
            ],
            'conv-1',
            'turn-1',
        );

        expect(row).toMatchObject({
            id: 'assistant-final',
            content: 'Your report summary is now open for entity 2656980.',
        });
    });

    it('prefers a standalone assistant message row over a tracker-backed lifecycle row for the same turn', () => {
        const tracker = new ConversationStreamTracker();
        tracker.applyEvent({
            type: 'model_started',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            messageId: 'msg-tracker',
            assistantMessageId: 'msg-tracker',
            patch: { status: 'thinking' },
        } as SSEEvent);

        const row = latestEffectiveLiveAssistantRow(
            tracker.canonicalState,
            [
                {
                    id: 'turn:turn-1',
                    role: 'assistant',
                    turnId: 'turn-1',
                    createdAt: '2026-01-01T00:00:03Z',
                    content: '',
                    interim: 1,
                    executionGroups: [
                        { pageId: 'turn:turn-1:lifecycle', turnId: 'turn-1', status: 'running' } as any,
                    ],
                },
                {
                    id: 'assistant-final',
                    role: 'assistant',
                    turnId: 'turn-1',
                    createdAt: '2026-01-01T00:00:02Z',
                    content: 'Your report summary is now open for entity 2656980.',
                    interim: 0,
                    _bubbleSource: 'message_add',
                    executionGroups: [],
                },
            ],
            'conv-1',
            'turn-1',
        );

        expect(row).toMatchObject({
            id: 'assistant-final',
            content: 'Your report summary is now open for entity 2656980.',
        });
    });
});
