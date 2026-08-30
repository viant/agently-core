import { describe, expect, it } from 'vitest';
import { applyFeedPatchOperations } from './feedPatch';

describe('applyFeedPatchOperations', () => {
    it('applies add, replace, and remove with JSON Pointer escaping', () => {
        const actual = applyFeedPatchOperations({ budget: 250000, tags: ['a'], nested: { 'a/b': 1 } }, [
            { dataSourceRef: 'draft', op: 'replace', path: '/budget', value: 260000 },
            { dataSourceRef: 'draft', op: 'add', path: '/tags/-', value: 'b' },
            { dataSourceRef: 'draft', op: 'remove', path: '/nested/a~1b' },
        ]);
        expect(actual).toEqual({ budget: 260000, tags: ['a', 'b'], nested: {} });
    });

    it('inserts numeric array additions without replacing the existing item', () => {
        const actual = applyFeedPatchOperations({ channels: ['CTV', 'DOOH'] }, [
            { dataSourceRef: 'draft', op: 'add', path: '/channels/1', value: 'Audio' },
        ]);
        expect(actual).toEqual({ channels: ['CTV', 'Audio', 'DOOH'] });
    });
});
