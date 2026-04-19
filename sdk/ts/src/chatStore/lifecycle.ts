/**
 * chatStore/lifecycle.ts — status → lifecycle mapping.
 *
 * Maps the six backend `api.TurnStatus` values (canonical.go) onto the five
 * client lifecycle states (§4.3). The mapping lives in the contract, not
 * just in tests. Tests verify the table matches.
 *
 * Mapping per ui-improvement.md §8 (hoisted from the test section):
 *
 *   queued            -> pending
 *   running           -> running
 *   waiting_for_user  -> running
 *   completed         -> completed
 *   failed            -> failed
 *   canceled          -> cancelled   (note backend spells it with one 'l')
 */

import type { CanonicalTurnStatus, ClientLifecycle } from './types';

/**
 * Total function from backend status to client lifecycle. An unrecognised
 * input value (future backend status the client has not yet learned about)
 * maps to `'running'` — the safest default during rollout, matching the
 * "latest turn is SSE-owned" intuition and letting the header show running
 * tone until an explicit terminal status arrives.
 */
export function statusToLifecycle(status: CanonicalTurnStatus | string | undefined): ClientLifecycle {
    switch (status) {
        case 'queued':
            return 'pending';
        case 'running':
            return 'running';
        case 'waiting_for_user':
            return 'running';
        case 'completed':
            return 'completed';
        case 'failed':
            return 'failed';
        case 'canceled':
            return 'cancelled';
        default:
            return 'running';
    }
}

/**
 * `true` iff a lifecycle represents a terminal state — no further live
 * updates are expected for the turn and ownership has transferred to
 * transcript (§4.4, §5.4).
 */
export function isTerminalLifecycle(lifecycle: ClientLifecycle): boolean {
    return lifecycle === 'completed' || lifecycle === 'failed' || lifecycle === 'cancelled';
}

/**
 * `true` iff a lifecycle represents a live / SSE-owned state. Mutually
 * exclusive with `isTerminalLifecycle`.
 */
export function isLiveLifecycle(lifecycle: ClientLifecycle): boolean {
    return lifecycle === 'pending' || lifecycle === 'running';
}
