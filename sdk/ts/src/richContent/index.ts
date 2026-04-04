/**
 * Rich content rendering utilities.
 *
 * Provides framework-agnostic parsing, classification, and markdown
 * rendering for assistant message content (code fences, charts, tables,
 * mermaid diagrams, inline markdown).
 *
 * UI frameworks register their own renderers via the pluggable registry.
 */

// Fence parsing
export { parseFences, languageHint } from './parseFences';
export type { FencePart } from './parseFences';

// Markdown table utilities
export {
  findNextPipeTableBlock,
  looksLikePipeTable,
  parsePipeTable,
} from './markdownTableUtils';
export type { TableBlock, ParsedTable } from './markdownTableUtils';

// Chart spec parsing
export {
  parseChartSpecFromFence,
  normalizeChartSpec,
  buildChartSeries,
} from './chartSpec';
export type {
  ChartSpec,
  ChartDef,
  ChartAxis,
  NormalizedChart,
  ChartSeries,
} from './chartSpec';

// Inline & block markdown rendering
export {
  escapeHTML,
  escapeHTMLAttr,
  resolveHref,
  inlineMarkdown,
  renderMarkdownCellHTML,
  renderMarkdownBlock,
} from './markdownInline';

// Pluggable renderer registry
export {
  FenceRendererRegistry,
  getDefaultRegistry,
  registerFenceRenderer,
  registerFenceClassifier,
} from './registry';
export type {
  BuiltinRendererType,
  FenceClassification,
  FenceClassifier,
} from './registry';

export {
  describeFence,
  describeFences,
  describeContent,
} from './descriptors';
export type {
  FenceDescriptor,
  RichContentDescriptor,
} from './descriptors';
