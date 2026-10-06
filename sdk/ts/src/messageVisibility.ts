/** Only declared internal lanes are hidden; task/standard JSON remains visible. */
export function isInternalMessageMode(mode: unknown): boolean {
    return typeof mode === 'string' && ['router', 'chain'].includes(mode.trim().toLowerCase());
}
