import { createHash } from "node:crypto";

export function buildFixtureDatasets(reportSpec) {
  const datasets = {};
  (Array.isArray(reportSpec?.datasets) ? reportSpec.datasets : []).forEach((dataset, index) => {
    const request = dataset?.request && typeof dataset.request === "object" ? dataset.request : {};
    const row = {};
    Object.keys(request.dimensions || {}).sort().forEach((key) => {
      const normalized = key.toLowerCase();
      row[key] = normalized.includes("date") || normalized.endsWith("_at") ? "2026-10-07" : `fixture-${key}`;
    });
    Object.keys(request.measures || {}).sort().forEach((key, measureIndex) => {
      row[key] = measureIndex + 10 + index;
    });
    Object.keys(request.filters || {}).sort().forEach((key) => {
      if (!Object.prototype.hasOwnProperty.call(row, key)) row[key] = request.filters[key];
    });
    datasets[dataset.id] = { rows: [row], hasMore: false, diagnostics: [] };
  });
  return datasets;
}

export function stableJSON(value) {
  if (Array.isArray(value)) return `[${value.map(stableJSON).join(",")}]`;
  if (value && typeof value === "object") {
    return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${stableJSON(value[key])}`).join(",")}}`;
  }
  return JSON.stringify(value);
}

export function stableFingerprint(value) {
  return createHash("sha256").update(stableJSON(value)).digest("hex");
}

export function rawJSONFingerprint(value) {
  return createHash("sha256").update(JSON.stringify(value)).digest("hex");
}

export function freezeFixtureDate(instant = "2026-10-07T12:00:00.000Z") {
  const NativeDate = globalThis.Date;
  const fixedTime = NativeDate.parse(instant);
  class FixtureDate extends NativeDate {
    constructor(...args) {
      super(...(args.length === 0 ? [fixedTime] : args));
    }
    static now() {
      return fixedTime;
    }
  }
  globalThis.Date = FixtureDate;
  return () => { globalThis.Date = NativeDate; };
}
