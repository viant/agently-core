import { describe, expect, it } from 'vitest';
import { renderMarkdownBlock } from '../richContent/markdownInline';

describe('authored entity chips', () => {
  it('retains the canonical id while displaying only the business label', () => {
    const html = renderMarkdownBlock('@{media_plan:plan_123 "Coca-Cola"} version 2');
    expect(html).toContain('class="agently-entity-chip"');
    expect(html).toContain('data-entity-id="plan_123"');
    expect(html).toContain('>Coca-Cola</span> version 2');
    expect(html).not.toContain('>plan_123<');
  });
});
