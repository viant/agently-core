import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

import { resolveActiveTurnProgress, resolveTurnNarration } from '../turnPresentation';

const fixturePath = fileURLToPath(new URL('../../../fixtures/turn_presentation.json', import.meta.url));
const fixtures = JSON.parse(readFileSync(fixturePath, 'utf8'));

describe('turn presentation golden fixtures', () => {
    for (const fixture of fixtures.progressCases) {
        it(fixture.name, () => {
            const actual = resolveActiveTurnProgress(fixture.input);
            if (fixture.expected == null) expect(actual).toBeNull();
            else expect(actual).toMatchObject(fixture.expected);
        });
    }

    for (const fixture of fixtures.narrationCases) {
        it(fixture.name, () => {
            const actual = resolveTurnNarration(fixture.input);
            if (fixture.expected == null) expect(actual).toBeNull();
            else expect(actual).toEqual(fixture.expected);
        });
    }
});
