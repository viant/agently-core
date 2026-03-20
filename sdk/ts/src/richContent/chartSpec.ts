/**
 * Chart specification parsing and normalization.
 * Detects JSON chart specs in fenced code blocks and normalizes them
 * to a canonical { chart, data, title } shape.
 */

export interface ChartAxis { key: string }
export interface ChartDef {
  type: string;
  x: ChartAxis;
  y: ChartAxis[];
  series?: { key: string };
  valueKey?: string;
}
export interface ChartSpec {
  chart: ChartDef;
  data: Record<string, unknown>[];
  title?: string;
  options?: { palette?: string[] };
  [key: string]: unknown;
}

export interface NormalizedChart {
  type: string;
  data: Record<string, unknown>[];
  xKey: string;
  seriesKey: string;
  yKeys: string[];
  valueKey: string;
  palette: string[];
  title: string;
}

export interface ChartSeries {
  rows: Record<string, unknown>[];
  series: string[];
}

function isObject(v: unknown): v is Record<string, unknown> {
  return !!v && typeof v === 'object' && !Array.isArray(v);
}

function inferFirstStringKey(row: Record<string, unknown>): string {
  const keys = Object.keys(row || {});
  return keys.find((k) => typeof row[k] === 'string') || keys[0] || 'x';
}

function inferFirstNumericKey(row: Record<string, unknown>, exclude: string[] = []): string {
  const deny = new Set(exclude);
  const keys = Object.keys(row || {});
  return keys.find((k) => !deny.has(k) && typeof row[k] === 'number')
    || keys.find((k) => !deny.has(k))
    || 'value';
}

/**
 * Try to parse a fenced code block body as a chart spec.
 * Returns `null` if the body is not a recognized chart JSON.
 */
export function parseChartSpecFromFence(lang: string, body: string): ChartSpec | null {
  const v = String(lang || '').toLowerCase();
  const raw = String(body || '')
    .replace(/[\u00A0\u1680\u2000-\u200B\u202F\u205F\u3000]/g, ' ')
    .trim();

  if (!['json', 'javascript', 'js', 'plaintext', 'md', 'markdown', 'chart'].includes(v)) return null;
  if (!raw.startsWith('{') && !raw.startsWith('[')) return null;

  try {
    const parsed = JSON.parse(raw);
    if (!isObject(parsed)) return null;

    // Shape 1: { chart: { type, x, y }, data: [...] }
    if (isObject(parsed.chart) && Array.isArray(parsed.data)) {
      return parsed as unknown as ChartSpec;
    }

    // Shape 2: { type, data: [...], x?, y? }
    const topType = String((parsed as Record<string, unknown>).type || '').trim().toLowerCase();
    if (topType && Array.isArray(parsed.data)) {
      const firstRow = isObject((parsed.data as unknown[])[0]) ? (parsed.data as Record<string, unknown>[])[0] : {};
      const xKey = String((parsed as any)?.x?.field || (parsed as any)?.xKey || inferFirstStringKey(firstRow));
      const yField = String((parsed as any)?.y?.field || '');
      const yKey = yField || inferFirstNumericKey(firstRow, [xKey]);
      const result: Record<string, unknown> = {
        chart: { type: topType, x: { key: xKey }, y: [{ key: yKey }] },
        data: parsed.data,
      };
      if (parsed.title != null) result.title = String(parsed.title);
      return result as unknown as ChartSpec;
    }

    return null;
  } catch {
    return null;
  }
}

const DEFAULT_PALETTE = ['#2563eb', '#ef4444', '#16a34a', '#f59e0b', '#9333ea', '#06b6d4'];

/**
 * Normalize a parsed chart spec into a flat rendering-friendly shape.
 */
export function normalizeChartSpec(spec: ChartSpec): NormalizedChart {
  const chart = isObject(spec.chart) ? spec.chart : {} as ChartDef;
  const type = String(chart.type || 'line').toLowerCase();
  const data = Array.isArray(spec.data) ? spec.data : [];
  const xKey = String(chart?.x?.key || (spec as any).xKey || 'x');
  const seriesKey = chart?.series?.key ? String(chart.series.key) : '';
  const yArr = Array.isArray(chart?.y) ? chart.y : [];
  const yKeys = yArr.map((v) => String(v?.key || '')).filter(Boolean);
  const valueKey = String(chart?.valueKey || (spec as any).valueKey || yKeys[0] || 'value');
  const palette = Array.isArray(spec?.options?.palette) ? spec.options!.palette! : DEFAULT_PALETTE;
  return { type, data, xKey, seriesKey, yKeys, valueKey, palette, title: String(spec.title || '') };
}

/**
 * Build pivoted chart rows and series keys from normalized chart spec.
 */
export function buildChartSeries(normalized: NormalizedChart): ChartSeries {
  const { data, xKey, seriesKey, yKeys, valueKey } = normalized;
  if (!Array.isArray(data) || data.length === 0) return { rows: [], series: [] };

  // Long form: x + series column + single value column
  if (seriesKey) {
    const map = new Map<string, Record<string, unknown>>();
    const order: string[] = [];
    for (const row of data) {
      const x = (row as Record<string, unknown>)[xKey];
      const s = String((row as Record<string, unknown>)[seriesKey] ?? '');
      if (!s) continue;
      if (!order.includes(s)) order.push(s);
      const k = String(x);
      if (!map.has(k)) map.set(k, { [xKey]: x });
      map.get(k)![s] = Number((row as Record<string, unknown>)[valueKey] ?? 0);
    }
    return { rows: Array.from(map.values()), series: order };
  }

  // Wide form: x + multiple y columns
  const keys = yKeys.length
    ? yKeys
    : Object.keys(data[0] || {}).filter((k) => k !== xKey && typeof (data[0] as Record<string, unknown>)[k] === 'number');
  return { rows: data, series: keys };
}
