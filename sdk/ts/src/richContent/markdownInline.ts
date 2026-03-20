/**
 * Inline and block-level markdown rendering to HTML strings.
 * Pure functions — no framework dependency.
 */

export function escapeHTML(value: string): string {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

export function escapeHTMLAttr(value: string): string {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

export function resolveHref(url: string): string {
  const raw = String(url || '').trim();
  if (!raw) return '#';
  if (raw.startsWith('/')) return raw;
  if (/^(https?:\/\/|mailto:|tel:)/i.test(raw)) return raw;
  return '#';
}

/**
 * Render inline markdown (code, bold, italic, strikethrough, links) on
 * an already HTML-escaped string.
 */
export function inlineMarkdown(escaped: string): string {
  let s = escaped;
  s = s.replace(/`([^`\n]+?)`/g, '<code>$1</code>');
  s = s.replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/\*(.*?)\*/g, '<em>$1</em>');
  s = s.replace(/(^|[^\w])_([^_\n]+)_/g, '$1<em>$2</em>');
  s = s.replace(/~~(.*?)~~/g, '<del>$1</del>');
  s = s.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, label, url) => {
    const href = resolveHref(url);
    return `<a href="${escapeHTMLAttr(href)}" target="_blank" rel="noopener noreferrer">${label}</a>`;
  });
  return s;
}

/**
 * Render markdown cell content (for table cells) — supports inline code,
 * bold, italic, strikethrough, links, and newlines.
 */
export function renderMarkdownCellHTML(md: string): string {
  const escaped = escapeHTML(md);
  let s = escaped.replace(/```([\s\S]*?)```/g, (_, p1) => `<pre><code>${p1}</code></pre>`);
  s = s.replace(/`([^`]+?)`/g, '<code>$1</code>');
  s = s.replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/\*(.*?)\*/g, '<em>$1</em>');
  s = s.replace(/~~(.*?)~~/g, '<del>$1</del>');
  s = s.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, label, url) => {
    const href = resolveHref(url);
    return `<a href="${escapeHTMLAttr(href)}" target="_blank" rel="noopener noreferrer">${label}</a>`;
  });
  return s.replace(/\n/g, '<br/>');
}

/**
 * Full block-level markdown rendering to HTML — handles headings, lists,
 * blockquotes, horizontal rules, tables (simple), and inline formatting.
 */
export function renderMarkdownBlock(value: string): string {
  const lines = String(value ?? '').split('\n');
  const blocks: string[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    // Horizontal rule
    if (/^(\s*[-*_]\s*){3,}$/.test(line)) {
      blocks.push('<hr/>');
      i++;
      continue;
    }

    // Unordered list
    if (/^\s*[-*+]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*[-*+]\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*[-*+]\s+/, ''));
        i++;
      }
      blocks.push(`<ul>${items.map((it) => `<li>${inlineMarkdown(escapeHTML(it))}</li>`).join('')}</ul>`);
      continue;
    }

    // Ordered list
    if (/^\s*\d+\.\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*\d+\.\s+/, ''));
        i++;
      }
      blocks.push(`<ol>${items.map((it) => `<li>${inlineMarkdown(escapeHTML(it))}</li>`).join('')}</ol>`);
      continue;
    }

    // Blockquote
    if (/^\s*>\s?/.test(line)) {
      const qLines: string[] = [];
      while (i < lines.length && /^\s*>\s?/.test(lines[i])) {
        qLines.push(lines[i].replace(/^\s*>\s?/, ''));
        i++;
      }
      blocks.push(`<blockquote>${inlineMarkdown(escapeHTML(qLines.join('\n')))}</blockquote>`);
      continue;
    }

    // Headings
    const headingMatch = line.match(/^(#{1,6})\s+(.+)$/);
    if (headingMatch) {
      const level = headingMatch[1].length;
      blocks.push(`<h${level}>${inlineMarkdown(escapeHTML(headingMatch[2]))}</h${level}>`);
      i++;
      continue;
    }

    // Plain text
    blocks.push(line.trim() === '' ? '<br/>' : inlineMarkdown(escapeHTML(line)));
    i++;
  }

  return blocks.join('\n');
}
