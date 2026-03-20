/**
 * GitHub-flavored Markdown pipe table detection and parsing.
 * Pure functions — no framework dependency.
 */

export interface TableBlock {
  start: number;
  end: number;
}

export interface ParsedTable {
  headers: string[];
  rows: string[][];
  aligns: ('left' | 'right' | 'center')[];
}

/**
 * Scan `text` starting from `fromIndex` for the next pipe-table block.
 * Returns `{ start, end }` or `null` when none found.
 */
export function findNextPipeTableBlock(text: string, fromIndex = 0): TableBlock | null {
  const src = String(text ?? '');
  const len = src.length;
  let i = Math.max(0, fromIndex | 0);

  while (i < len) {
    const lineStart = i;
    const nextNl = src.indexOf('\n', i);
    const lineEnd = nextNl === -1 ? len : nextNl;
    const line = src.slice(lineStart, lineEnd).trim();

    if (line.includes('|')) {
      let j = lineEnd + 1;
      while (j < len) {
        const nl = src.indexOf('\n', j);
        const e = nl === -1 ? len : nl;
        const l = src.slice(j, e).trim();
        if (l.length === 0) { j = e + 1; continue; }
        const sepOk = /^\|?\s*[:\-]+(\s*\|\s*[:\-]+)+\s*\|?$/.test(l);
        if (!sepOk) break;

        let k = e + 1;
        let last = k;
        while (k <= len) {
          const nl2 = src.indexOf('\n', k);
          const e2 = nl2 === -1 ? len : nl2;
          const l2 = src.slice(k, e2).trim();
          if (l2.length === 0) { last = e2 + 1; break; }
          if (!l2.includes('|')) { last = k; break; }
          last = e2 + 1;
          if (nl2 === -1) break;
          k = e2 + 1;
        }
        return { start: lineStart, end: Math.min(last, len) };
      }
    }
    if (nextNl === -1) break;
    i = nextNl + 1;
  }
  return null;
}

/**
 * Quick check: does `body` look like a pipe table (header + separator)?
 */
export function looksLikePipeTable(body: string): boolean {
  const lines = String(body ?? '').split('\n').map(l => l.trim()).filter(l => l.length > 0);
  if (lines.length < 2) return false;
  if (!lines[0].includes('|')) return false;
  return /^\|?\s*[:\-]+(\s*\|\s*[:\-]+)+\s*\|?$/.test(lines[1]);
}

/**
 * Parse a pipe table into structured data.
 */
export function parsePipeTable(body: string): ParsedTable {
  const lines = String(body ?? '').split('\n').map(l => l.trim()).filter(l => l.length > 0);
  const headerLine = lines[0] || '';
  const sepLine = lines[1] || '';
  const dataLines = lines.slice(2);

  const toCells = (line: string): string[] => {
    let s = line;
    if (s.startsWith('|')) s = s.slice(1);
    if (s.endsWith('|')) s = s.slice(0, -1);
    return s.split('|').map(c => c.trim());
  };

  const headers = toCells(headerLine);

  const aligns: ('left' | 'right' | 'center')[] = (() => {
    let s = sepLine;
    if (s.startsWith('|')) s = s.slice(1);
    if (s.endsWith('|')) s = s.slice(0, -1);
    const segs = s.split('|');
    const parseAlign = (seg: string): 'left' | 'right' | 'center' => {
      const x = (seg || '').trim();
      const left = x.startsWith(':');
      const right = x.endsWith(':');
      if (left && right) return 'center';
      if (right) return 'right';
      return 'left';
    };
    const arr = segs.map(parseAlign);
    while (arr.length < headers.length) arr.push('left');
    if (arr.length > headers.length) arr.length = headers.length;
    return arr;
  })();

  const rows = dataLines.map(toCells).map(r => {
    if (r.length < headers.length) return r.concat(Array(headers.length - r.length).fill(''));
    if (r.length > headers.length) return r.slice(0, headers.length);
    return r;
  });

  return { headers, rows, aligns };
}
