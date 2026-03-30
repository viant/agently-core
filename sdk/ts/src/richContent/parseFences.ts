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
  const pattern = new RegExp('```' + FENCE_LANG + FENCE_BODY_START + '([\\s\\S]*?)```', 'g');
  let index = 0;
  let match: RegExpExecArray | null;

  while ((match = pattern.exec(text)) !== null) {
    if (match.index > index) {
      result.push({ kind: 'text', value: text.slice(index, match.index) });
    }
    result.push({
      kind: 'fence',
      lang: String(match[1] || '').trim().toLowerCase(),
      body: String(match[2] || ''),
    });
    index = pattern.lastIndex;
  }

  if (index < text.length) {
    const tail = text.slice(index);
    // Detect an unclosed fence (streaming in progress).
    const openFence = tail.match(new RegExp('^```' + FENCE_LANG + FENCE_BODY_START + '([\\s\\S]*)$'));
    if (openFence) {
      result.push({
        kind: 'fence',
        lang: String(openFence[1] || '').trim().toLowerCase(),
        body: String(openFence[2] || ''),
      });
    } else {
      result.push({ kind: 'text', value: tail });
    }
  }

  return result;
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
