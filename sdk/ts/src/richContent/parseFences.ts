/**
 * Fence parsing — splits raw markdown content into alternating text and
 * fenced code block segments.  Pure function, no framework dependency.
 */

export interface FencePart {
  kind: 'text' | 'fence';
  lang?: string;
  body?: string;
  value?: string;
}

const FENCE_LANG = '([a-zA-Z0-9_+\\-]*)';
const FENCE_BODY_START = '(?:\\r?\\n|(?=[\\[{]))';

/**
 * Split `content` into an ordered array of text and fenced-code parts.
 * Handles both closed (```…```) and still-open (streaming) fences.
 *
 * Some streamed outputs collapse the newline between a language tag and a JSON
 * payload into ` ```json{ `. We tolerate that form by allowing `{` or `[` to
 * start the fence body directly after the language token.
 */
export function parseFences(content: string): FencePart[] {
  const text = String(content ?? '');
  const result: FencePart[] = [];
  const opening = new RegExp('```' + FENCE_LANG + FENCE_BODY_START, 'g');
  let index = 0;
  let match: RegExpExecArray | null;

  while ((match = opening.exec(text)) !== null) {
    if (match.index > index) {
      result.push({ kind: 'text', value: text.slice(index, match.index) });
    }
    const lang = String(match[1] || '').trim().toLowerCase();
    const bodyStart = opening.lastIndex;
    const closing = closingFenceIndex(text, bodyStart, lang);
    result.push({
      kind: 'fence',
      lang,
      body: text.slice(bodyStart, closing < 0 ? text.length : closing),
    });
    if (closing < 0) return result;
    index = closing + 3;
    opening.lastIndex = index;
  }

  if (index < text.length) {
    result.push({ kind: 'text', value: text.slice(index) });
  }

  return result;
}

/** Report JSON strings may embed Markdown code blocks. Their backticks belong
 * to the payload, including while a quoted/escaped string is still streaming.
 * Ordinary code fences keep their existing first-delimiter behavior.
 */
function closingFenceIndex(text: string, bodyStart: number, lang: string): number {
  if (lang !== 'forge-report' || !/^\s*[\[{]/.test(text.slice(bodyStart))) {
    return text.indexOf('```', bodyStart);
  }
  let quoted = false;
  let escaped = false;
  for (let index = bodyStart; index < text.length; index++) {
    const character = text[index];
    if (quoted) {
      if (escaped) escaped = false;
      else if (character === '\\') escaped = true;
      else if (character === '"') quoted = false;
    } else if (character === '"') {
      quoted = true;
    } else if (character === '`' && text.startsWith('```', index)) {
      return index;
    }
  }
  return -1;
}

/**
 * Normalize a raw language hint to a canonical name.
 */
export function languageHint(lang: string): string {
  const v = String(lang || '').trim().toLowerCase();
  if (!v) return 'plaintext';
  if (v === 'js') return 'javascript';
  if (v === 'ts') return 'typescript';
  if (v === 'yml') return 'yaml';
  if (v === 'sequence' || v === 'sequencediagram') return 'mermaid';
  return v;
}
