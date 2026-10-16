import { describe, it, expect } from 'vitest';
import { renderMarkdownBlock } from '../richContent/markdownInline';

describe('elicitation Markdown presentation', () => {
  it('renders prose, lists, and fenced examples without interpreting code as markup', () => {
    const html = renderMarkdownBlock('## Review changes\n**Campaign** needs *approval*.\n- First item\n- Second item\n\n```json\n{"example":"**literal**"}\n```');
    expect(html).toContain('<h2>Review changes</h2>');
    expect(html).toContain('<strong>Campaign</strong>');
    expect(html).toContain('<em>approval</em>');
    expect(html).toContain('<li>First item</li>');
    expect(html).toContain('<code>{"example":"**literal**"}</code>');
  });
  it('renders comparison tables and keeps inline code literal', () => {
    const html = renderMarkdownBlock('| Field | Value |\n| --- | --- |\n| Budget | `**literal**` |');
    expect(html).toContain('<th>Field</th>');
    expect(html).toContain('<td><code>**literal**</code></td>');
  });
  it('renders the full escaped campaign name and all five cells', () => {
    const html = renderMarkdownBlock([
      '| Rank | Campaign | Campaign ID | Bid potential | Expected bids |',
      '| --- | --- | --- | --- | --- |',
      String.raw`| 3 | UOT Ski + Winter FY26/27 - Airports \| DOOH | 561867 | 8.560x | 55,404 |`,
    ].join('\n'));
    expect(html.match(/<td>.*?<\/td>/g)).toEqual([
      '<td>3</td>',
      '<td>UOT Ski + Winter FY26/27 - Airports | DOOH</td>',
      '<td>561867</td>',
      '<td>8.560x</td>',
      '<td>55,404</td>',
    ]);
  });
  it('uses the same escaped header and row boundaries for HTML tables', () => {
    const html = renderMarkdownBlock(String.raw`Name \| label | Value
:--- | ---:
\|A | B\|
C\\|D
||`);
    expect(html).toContain('<th>Name | label</th>');
    expect(html).toContain('<tr><td>|A</td><td>B|</td></tr>');
    expect(html).toContain(String.raw`<tr><td>C\\</td><td>D</td></tr>`);
    expect(html).toContain('<tr><td></td><td></td></tr>');
  });
  it('escapes HTML and blocks executable link schemes', () => {
    const html = renderMarkdownBlock('<script>alert(1)</script>\n[Run](javascript:alert)\n[Docs](https://example.com/help)');
    expect(html).not.toContain('<script>');
    expect(html).not.toContain('href="javascript:');
    expect(html).toContain('href="https://example.com/help"');
  });
});
