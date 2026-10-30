import { describe, it, expect } from 'vitest';
import { renderMarkdownBlock } from '../richContent/markdownInline';

describe('elicitation Markdown presentation', () => {
  it('uses paragraph blocks instead of stacking blank-line breaks', () => {
    expect(renderMarkdownBlock('\nFirst paragraph.\n\n\nSecond paragraph.\n'))
      .toBe('<p>First paragraph.</p>\n<p>Second paragraph.</p>');
    expect(renderMarkdownBlock(' \n\t\n')).toBe('');
  });

  it('preserves deliberate line breaks within paragraphs and quotes', () => {
    expect(renderMarkdownBlock('First line.\nSecond **line**.\n\n> Quoted line.\n> Next line.'))
      .toBe('<p>First line.<br/>Second <strong>line</strong>.</p>\n<blockquote>Quoted line.<br/>Next line.</blockquote>');
  });

  it.each([
    ['## Heading', '<h2>Heading</h2>'],
    ['- Item', '<ul><li>Item</li></ul>'],
    ['1. Item', '<ol><li>Item</li></ol>'],
    ['---', '<hr/>'],
    ['> Quote', '<blockquote>Quote</blockquote>'],
  ])('separates prose from an adjacent block: %s', (markdown, block) => {
    expect(renderMarkdownBlock(`Before.\n${markdown}\nAfter.`))
      .toBe(`<p>Before.</p>\n${block}\n<p>After.</p>`);
  });

  it('keeps code whitespace and table structure outside paragraphs', () => {
    const html = renderMarkdownBlock('Before.\n```text\n  <literal>\n\n    indented\n```\nAfter.\n| Name | Value |\n| --- | --- |\n| Example | 2 |');
    expect(html).toContain('<p>Before.</p>\n<pre');
    expect(html).toContain('<code>  &lt;literal&gt;\n\n    indented</code></pre>\n<p>After.</p>\n<table>');
    expect(html).toContain('<td>Example</td><td>2</td>');
    expect(html).not.toContain('<p><pre');
    expect(html).not.toContain('<p><table');
  });

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
