import { expect, it } from 'vitest';
import { applyEvent, applyTranscript, newConversationState } from '../chatStore/reducer';
import { projectConversation, type IterationRenderRow } from '../chatStore/projector';

it('preserves OAuth connection mode and URL through event and transcript projection', () => {
    const url = '/v1/api/auth/mcp/asana/initiate';
    let eventState = applyEvent(newConversationState('c'), {type: 'turn_started', conversationId: 'c', turnId: 't'});
    eventState = applyEvent(eventState, {type: 'elicitation_requested', conversationId: 'c', turnId: 't', elicitationId: 'connect', status: 'pending', elicitationData: {mode: 'mcp_oauth', url, requestedSchema: {type: 'object'}}});
    const transcriptState = applyTranscript(newConversationState('c'), {conversationId: 'c', turns: [{turnId: 't', status: 'waiting_for_user', elicitation: {elicitationId: 'connect', status: 'pending', mode: 'mcp_oauth', url, requestedSchema: {type: 'object'}}}]});
    for (const state of [eventState, transcriptState]) {
        const row = projectConversation(state).find(row => 'elicitation' in row) as IterationRenderRow;
        expect(row.elicitation).toMatchObject({mode: 'mcp_oauth', url});
    }
});
