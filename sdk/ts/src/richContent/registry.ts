/**
 * Pluggable fence renderer registry.
 *
 * Apps register renderer functions keyed by a renderer type string.
 * The registry maps fence languages to renderer types and provides
 * a classification pipeline to determine how each fence should render.
 *
 * This is framework-agnostic — renderers are opaque values (e.g. React
 * components, Svelte components, or plain functions).
 */

import { looksLikePipeTable } from './markdownTableUtils';
import { parseChartSpecFromFence, type ChartSpec } from './chartSpec';
import { languageHint } from './parseFences';

/** Built-in renderer type identifiers. */
export type BuiltinRendererType =
  | 'code'
  | 'mermaid'
  | 'chart'
  | 'pipeTable'
  | 'markdown'
  | 'prose';

/** Result of classifying a fenced block — tells the UI *what* to render. */
export interface FenceClassification {
  renderer: string;       // renderer type key (built-in or custom)
  lang: string;           // normalized language
  body: string;           // raw fence body
  chartSpec?: ChartSpec;  // present when renderer === 'chart'
}

/** A custom classifier function. Return a renderer key or null to skip. */
export type FenceClassifier = (lang: string, body: string) => string | null;

/**
 * Registry for fence renderers and classifiers.
 */
export class FenceRendererRegistry {
  /** Map of renderer key → renderer value (framework-specific component/function). */
  private renderers = new Map<string, unknown>();

  /** Ordered list of custom classifiers (checked before built-in logic). */
  private classifiers: FenceClassifier[] = [];

  /** Map of language → forced renderer key (overrides built-in classification). */
  private langOverrides = new Map<string, string>();

  /**
   * Register a renderer for a given type key.
   * The `renderer` value is opaque — typically a React component, but could
   * be anything the consuming framework understands.
   */
  registerRenderer(type: string, renderer: unknown): void {
    this.renderers.set(type, renderer);
  }

  /** Get a registered renderer by type key. */
  getRenderer<T = unknown>(type: string): T | undefined {
    return this.renderers.get(type) as T | undefined;
  }

  /** Check if a renderer is registered for the given type. */
  hasRenderer(type: string): boolean {
    return this.renderers.has(type);
  }

  /**
   * Register a custom classifier that runs before built-in logic.
   * Return a renderer key string to claim the fence, or null to pass.
   */
  registerClassifier(classifier: FenceClassifier): void {
    this.classifiers.push(classifier);
  }

  /**
   * Force all fences with `lang` to use the given renderer type,
   * bypassing built-in classification.
   */
  setLanguageRenderer(lang: string, rendererType: string): void {
    this.langOverrides.set(lang.toLowerCase(), rendererType);
  }

  /**
   * Classify a fenced code block into a rendering instruction.
   */
  classify(rawLang: string, body: string): FenceClassification {
    const lang = languageHint(rawLang);

    // 1. Custom classifiers (highest priority)
    for (const classifier of this.classifiers) {
      const result = classifier(lang, body);
      if (result) return { renderer: result, lang, body };
    }

    // 2. Per-language overrides
    const override = this.langOverrides.get(lang);
    if (override) return { renderer: override, lang, body };

    // 3. Chart spec detection
    const chartSpec = parseChartSpecFromFence(lang, body);
    if (chartSpec) return { renderer: 'chart', lang, body, chartSpec };

    // 4. Pipe table in markdown/plaintext fence
    if ((lang === 'markdown' || lang === 'md' || lang === 'plaintext') && looksLikePipeTable(body)) {
      return { renderer: 'pipeTable', lang, body };
    }

    // 5. Mermaid diagrams
    if (lang === 'mermaid' || /^\s*(sequenceDiagram|flowchart|graph|classDiagram|stateDiagram)/.test(body)) {
      return { renderer: 'mermaid', lang, body };
    }

    // 6. Markdown fence → render as prose
    if (lang === 'markdown' || lang === 'md') {
      return { renderer: 'markdown', lang, body };
    }

    // 7. Default: syntax-highlighted code block
    return { renderer: 'code', lang, body };
  }
}

/** Shared default registry instance. */
let defaultRegistry: FenceRendererRegistry | null = null;

/**
 * Get (or create) the default shared registry.
 */
export function getDefaultRegistry(): FenceRendererRegistry {
  if (!defaultRegistry) {
    defaultRegistry = new FenceRendererRegistry();
  }
  return defaultRegistry;
}

/**
 * Convenience: register a renderer on the default registry.
 */
export function registerFenceRenderer(type: string, renderer: unknown): void {
  getDefaultRegistry().registerRenderer(type, renderer);
}

/**
 * Convenience: register a custom classifier on the default registry.
 */
export function registerFenceClassifier(classifier: FenceClassifier): void {
  getDefaultRegistry().registerClassifier(classifier);
}
