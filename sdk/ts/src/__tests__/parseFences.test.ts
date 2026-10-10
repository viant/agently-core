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

describe('structured report fences', () => {
  const payload = {
    version: 1,
    blocks: [{
      kind: 'markdownBlock',
      markdown: 'Report excerpt:\n```text\n136934, 150180\n```\nA "quoted" label, \\path, and literal {braces}.',
    }],
  };

  it('keeps nested markdown code fences inside report JSON and preserves surrounding prose', () => {
    const body = JSON.stringify(payload);
    expect(parseFences(`Before\n\`\`\`forge-report\n${body}\n\`\`\`\nAfter`)).toEqual([
      { kind: 'text', value: 'Before\n' },
      { kind: 'fence', lang: 'forge-report', body: `${body}\n` },
      { kind: 'text', value: '\nAfter' },
    ]);
  });

  it('supports compact report arrays with nested escaped strings', () => {
    const body = JSON.stringify([payload, { markdown: 'Escaped quote: "```text" and \\"```"' }]);
    expect(parseFences(`\`\`\`forge-report${body}\`\`\``)).toEqual([
      { kind: 'fence', lang: 'forge-report', body },
    ]);
  });

  it('keeps partial JSON inside one streaming report fence at every chunk boundary', () => {
    const body = JSON.stringify(payload);
    for (let end = 1; end <= body.length; end++) {
      const partial = body.slice(0, end);
      expect(parseFences(`Before\n\`\`\`forge-report\n${partial}`), `payload prefix ${end}`).toEqual([
        { kind: 'text', value: 'Before\n' },
        { kind: 'fence', lang: 'forge-report', body: partial },
      ]);
    }
  });

  it('keeps adjacent report updates and CRLF fence boundaries separate', () => {
    const body = JSON.stringify(payload);
    const commit = '{"version":1,"mode":"commit"}';
    expect(parseFences(`\`\`\`forge-report\r\n${body}\r\n\`\`\`\r\n\`\`\`forge-report${commit}\`\`\``)).toEqual([
      { kind: 'fence', lang: 'forge-report', body: `${body}\r\n` },
      { kind: 'text', value: '\r\n' },
      { kind: 'fence', lang: 'forge-report', body: commit },
    ]);
  });

  it('preserves legacy non-JSON report fences and ordinary inline code delimiters', () => {
    expect(parseFences('```forge-report\nLegacy report\n```')).toEqual([
      { kind: 'fence', lang: 'forge-report', body: 'Legacy report\n' },
    ]);
    expect(parseFences('```js\nconst text = "```";')).toEqual([
      { kind: 'fence', lang: 'js', body: 'const text = "' },
      { kind: 'text', value: '";' },
    ]);
  });

  it('preserves ordinary code fences before and after a nested-code report', () => {
    const body = JSON.stringify(payload);
    expect(parseFences(`\`\`\`js\nconst x = 1;\n\`\`\`\n\`\`\`forge-report\n${body}\n\`\`\`\n\`\`\`text\nDone\n\`\`\``)).toEqual([
      { kind: 'fence', lang: 'js', body: 'const x = 1;\n' },
      { kind: 'text', value: '\n' },
      { kind: 'fence', lang: 'forge-report', body: `${body}\n` },
      { kind: 'text', value: '\n' },
      { kind: 'fence', lang: 'text', body: 'Done\n' },
    ]);
  });
});
