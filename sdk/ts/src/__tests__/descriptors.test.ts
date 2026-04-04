import { describe, expect, it } from 'vitest';
import { describeContent, describeFence, describeFences } from '../richContent/descriptors';
import { parseFences } from '../richContent/parseFences';

describe('rich content fence descriptors', () => {
  it('describes markdown fences as markdown renderer', () => {
    const fence = describeFence('markdown', '# Title');
    expect(fence.renderer).toBe('markdown');
    expect(fence.lang).toBe('markdown');
  });

  it('describes pipe table fences with parsed table metadata', () => {
    const fence = describeFence('markdown', '| Name | Value |\n| --- | --- |\n| A | 1 |');
    expect(fence.renderer).toBe('pipeTable');
    expect(fence.table?.headers).toEqual(['Name', 'Value']);
    expect(fence.table?.rows).toEqual([['A', '1']]);
  });

  it('describes mermaid fences from body signature even without lang', () => {
    const fence = describeFence('', 'flowchart TD\nA[Start] --> B[Done]');
    expect(fence.renderer).toBe('mermaid');
  });

  it('describes chart fences with chart spec payload', () => {
    const fence = describeFence('json', '{"chart":{"type":"bar","x":{"key":"name"},"y":[{"key":"value"}]},"data":[{"name":"A","value":1}]}');
    expect(fence.renderer).toBe('chart');
    expect(fence.chartSpec?.chart.type).toBe('bar');
  });

  it('maps parsed fence parts to normalized descriptors', () => {
    const parts = parseFences('Before\n```markdown\n# Hello\n```\nAfter');
    const described = describeFences(parts);
    expect(described).toHaveLength(3);
    expect(described[1]).toMatchObject({
      kind: 'fence',
      fence: { renderer: 'markdown', body: '# Hello\n' },
    });
  });

  it('describes plain-text pipe tables as table descriptors', () => {
    const described = describeContent('Intro\n\n| Name | Value |\n| --- | --- |\n| A | 1 |\n\nTail');
    expect(described).toHaveLength(3);
    expect(described[0]).toMatchObject({ kind: 'text' });
    expect(described[1]).toMatchObject({
      kind: 'table',
      table: {
        headers: ['Name', 'Value'],
        rows: [['A', '1']],
      },
    });
    expect(described[2]).toMatchObject({ kind: 'text' });
  });

  it('preserves mixed prose, tables, and fenced rich content in order', () => {
    const described = describeContent([
      'Intro',
      '',
      '| Name | Value |',
      '| --- | --- |',
      '| A | 1 |',
      '',
      '```mermaid',
      'flowchart TD',
      'A[Start] --> B[Done]',
      '```',
      '',
      '```json',
      '{"chart":{"type":"bar","x":{"key":"name"},"y":[{"key":"value"}]},"data":[{"name":"A","value":1}]}',
      '```',
      '',
      'Tail',
    ].join('\n'));

    expect(described.map((entry) => entry.kind === 'fence' ? entry.fence.renderer : entry.kind)).toEqual([
      'text',
      'table',
      'mermaid',
      'text',
      'chart',
      'text',
    ]);
    expect(described[2]).toMatchObject({
      kind: 'fence',
      fence: { renderer: 'mermaid' },
    });
    expect(described[4]).toMatchObject({
      kind: 'fence',
      fence: { renderer: 'chart' },
    });
  });
});
