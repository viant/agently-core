import { parseFences } from './parseFences';
import { findNextPipeTableBlock, looksLikePipeTable, parsePipeTable, type ParsedTable } from './markdownTableUtils';
import { parseChartSpecFromFence, type ChartSpec } from './chartSpec';
import { getDefaultRegistry, type FenceClassification, type FenceRendererRegistry } from './registry';

export interface FenceDescriptor extends FenceClassification {
  table?: ParsedTable;
}

export type RichContentDescriptor =
  | { kind: 'text'; value: string }
  | { kind: 'table'; raw: string; table: ParsedTable }
  | { kind: 'fence'; fence: FenceDescriptor };

export function describeFence(
  rawLang: string,
  body: string,
  registry: FenceRendererRegistry = getDefaultRegistry(),
): FenceDescriptor {
  const classification = registry.classify(rawLang, body);
  return enrichFenceDescriptor(classification);
}

export function describeFences(
  parts: Array<{ kind: 'text' | 'fence'; value?: string; lang?: string; body?: string }>,
  registry: FenceRendererRegistry = getDefaultRegistry(),
): RichContentDescriptor[] {
  return parts.map((part) => {
    if (part.kind === 'text') {
      return { kind: 'text', value: String(part.value ?? '') };
    }
    return {
      kind: 'fence',
      fence: describeFence(String(part.lang ?? ''), String(part.body ?? ''), registry),
    };
  });
}

export function describeContent(
  content: string,
  registry: FenceRendererRegistry = getDefaultRegistry(),
): RichContentDescriptor[] {
  const parts = parseFences(String(content ?? ''));
  const out: RichContentDescriptor[] = [];

  for (const part of parts) {
    if (part.kind === 'text') {
      out.push(...describePlainText(String(part.value ?? '')));
      continue;
    }
    out.push({
      kind: 'fence',
      fence: describeFence(String(part.lang ?? ''), String(part.body ?? ''), registry),
    });
  }

  return out;
}

function describePlainText(text: string): RichContentDescriptor[] {
  const chunk = String(text ?? '');
  if (!chunk) return [];

  const out: RichContentDescriptor[] = [];
  let cursor = 0;

  while (true) {
    const block = findNextPipeTableBlock(chunk, cursor);
    if (!block) break;
    if (block.start > cursor) {
      out.push({ kind: 'text', value: chunk.slice(cursor, block.start) });
    }
    const raw = chunk.slice(block.start, block.end);
    out.push({
      kind: 'table',
      raw,
      table: parsePipeTable(raw),
    });
    cursor = block.end;
  }

  if (cursor < chunk.length) {
    out.push({ kind: 'text', value: chunk.slice(cursor) });
  }

  return out;
}

function enrichFenceDescriptor(classification: FenceClassification): FenceDescriptor {
  const fence: FenceDescriptor = { ...classification };
  if (classification.renderer === 'pipeTable') {
    fence.table = parsePipeTable(classification.body);
  }
  if (classification.renderer === 'chart' && !classification.chartSpec) {
    const chartSpec = parseChartSpecFromFence(classification.lang, classification.body);
    if (chartSpec != null) {
      fence.chartSpec = chartSpec as ChartSpec;
    }
  }
  if (!fence.table && (classification.lang === 'markdown' || classification.lang === 'md' || classification.lang === 'plaintext')
    && looksLikePipeTable(classification.body)) {
    fence.table = parsePipeTable(classification.body);
  }
  return fence;
}
