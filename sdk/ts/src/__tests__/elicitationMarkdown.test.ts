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
  it('escapes HTML and blocks executable link schemes', () => {
    const html = renderMarkdownBlock('<script>alert(1)</script>\n[Run](javascript:alert)\n[Docs](https://example.com/help)');
    expect(html).not.toContain('<script>');
    expect(html).not.toContain('href="javascript:');
    expect(html).toContain('href="https://example.com/help"');
  });
});
