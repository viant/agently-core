import { describe, expect, it } from 'vitest';

import { parseFences } from '../richContent/parseFences';

describe('parseFences', () => {
  it('parses a standard fenced block with newline after language', () => {
    const parts = parseFences('Before\n```json\n{"ok":true}\n```\nAfter');

    expect(parts).toHaveLength(3);
    expect(parts[1]).toMatchObject({
      kind: 'fence',
      lang: 'json',
      body: '{"ok":true}\n',
    });
  });

  it('parses a fenced block when JSON body starts immediately after language', () => {
    const parts = parseFences('<!-- CHART_SPEC:v1 -->\n```json{"version":"1.0"}\n```');

    expect(parts).toHaveLength(2);
    expect(parts[1]).toMatchObject({
      kind: 'fence',
      lang: 'json',
      body: '{"version":"1.0"}\n',
    });
  });

  it('parses an unterminated streaming fence in compact JSON form', () => {
    const parts = parseFences('```json{"a":1, "b":');

    expect(parts).toHaveLength(1);
    expect(parts[0]).toMatchObject({
      kind: 'fence',
      lang: 'json',
    });
    expect(parts[0].body).toBe('{"a":1, "b":');
  });

  it('preserves prose before an unterminated streaming fence', () => {
    const parts = parseFences('### Key findings\n- Keep this visible.\n\n```forge-report\n{"version":1');

    expect(parts).toEqual([
      { kind: 'text', value: '### Key findings\n- Keep this visible.\n\n' },
      { kind: 'fence', lang: 'forge-report', body: '{"version":1' },
    ]);
  });
});
