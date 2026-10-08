import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";

import "../lowerer.bundle.js";
import {
  buildReportBuilderReportDocument,
  lowerReportDocumentToReportSpec,
} from "../../../../../forge/src/reporting/reportDocumentModel.js";
import { buildReportFillFromReportSpec } from "../../../../../forge/src/reporting/reportFillModel.js";
import { buildReportPrintFromReportFill } from "../../../../../forge/src/reporting/reportPrintModel.js";
import { buildFixtureDatasets, freezeFixtureDate, rawJSONFingerprint, stableFingerprint } from "./materialization-fixtures.mjs";

freezeFixtureDate();

function referenceLower(envelope) {
  const config = structuredClone(envelope.builderDefinition.reportBuilder);
  delete config.document;
  delete config.reportDocument;
  delete config.state;
  const sources = Object.keys(envelope.dataSources || {}).sort().map((id) => ({
    ...structuredClone(envelope.dataSources[id]),
    id,
    dataSourceRef: envelope.dataSources[id].dataSourceRef || envelope.dataSources[id].value || id,
  }));
  if (sources.length > 0 && (!Array.isArray(config.dataSources) || config.dataSources.length===0)) config.dataSources = sources;
  const sourceDocument = envelope.reportDocument;
  const sourceBlock = sourceDocument.blocks.find((block) => block.kind === "reportBuilderBlock") || null;
  const containerId = sourceBlock?.source?.containerId || sourceBlock?.source?.stateKey || sourceDocument.id || envelope.builderRef;
  const generated = buildReportBuilderReportDocument({
    container: {
      id: containerId,
      stateKey: sourceBlock?.source?.stateKey || containerId,
      title: sourceDocument.title || sourceBlock?.title || config.title || envelope.builderDefinition.title || "Report",
      dataSourceRef: sourceBlock?.source?.dataSourceRef || sourceDocument.scope?.dataSourceRef || config.dataSourceRef || envelope.builderDefinition.dataSourceRef || "",
    },
    config,
    state: envelope.state || {},
    additionalBlocks: sourceDocument.blocks.filter((block) => block.kind !== "reportBuilderBlock"),
    refinements: sourceDocument.refinements || [],
    semanticSummary: sourceDocument.semanticSummary || null,
  });
  const builderBlock = generated.blocks.find((block) => block.kind === "reportBuilderBlock");
  const blocks = (sourceDocument.blocks || []).map((block) => block.kind === "reportBuilderBlock" ? structuredClone(builderBlock) : structuredClone(block));
  if (!blocks.some((block) => block.kind === "reportBuilderBlock")) blocks.push(structuredClone(builderBlock));
  const expectedDocument = { ...generated, ...structuredClone(sourceDocument), blocks };
  return lowerReportDocumentToReportSpec(expectedDocument);
}

const sample = JSON.parse(await readFile(new URL("../testdata/authored-envelope.json", import.meta.url), "utf8"));
const cases = [
  ["authored-envelope.json", sample],
  ...(await readdir(new URL("../testdata/steward/", import.meta.url))).sort().map(async (file) => [
    file,
    JSON.parse(await readFile(new URL(`../testdata/steward/${file}`, import.meta.url), "utf8")),
  ]),
];
const resolvedCases = await Promise.all(cases);
const materializationGolden = JSON.parse(await readFile(new URL("../testdata/steward_materialization/expected.json", import.meta.url), "utf8"));
for (const [name, envelope] of resolvedCases) {
  const actual = JSON.parse(globalThis.ForgeAuthoredLowerer.lower(JSON.stringify(envelope)));
  assert.deepEqual(actual, referenceLower(envelope), `${name}: must match the official report lowerer`);
  if (name !== "authored-envelope.json") {
    const datasets = buildFixtureDatasets(actual);
    const reportFill = buildReportFillFromReportSpec(actual, datasets);
    const reportPrint = buildReportPrintFromReportFill({ reportSpec: actual, reportFill });
    const materialized = JSON.parse(globalThis.ForgeAuthoredLowerer.materialize(JSON.stringify({ reportSpec: actual, datasets })));
    assert.deepEqual(materialized, { reportFill, reportPrint }, `${name}: Goja bundle must match official fill/print models`);
    assert.equal(stableFingerprint(actual), materializationGolden[name].reportSpec, `${name}: reportSpec golden drift`);
    assert.equal(stableFingerprint(reportFill), materializationGolden[name].reportFill, `${name}: reportFill golden drift`);
    assert.equal(rawJSONFingerprint(reportPrint), materializationGolden[name].reportPrint, `${name}: reportPrint golden drift`);
  }
  if (name !== "authored-envelope.json") {
    assert.equal(actual.kind, "reportSpec", `${name}: missing compiled ReportSpec`);
    const primary = actual.datasets.find((dataset) => dataset.id === "primary");
    assert.ok(primary?.dataSourceRef, `${name}: primary datasource must remain wired`);
    assert.equal(primary.dataSourceRef, envelope.builderDefinition.dataSourceRef, `${name}: primary datasource drifted`);
  }
}
const actual = JSON.parse(globalThis.ForgeAuthoredLowerer.lower(JSON.stringify(sample)));
assert.equal(actual.title, "Delivery Performance");
assert.equal(actual.source.dataSourceRef, "steward-orders");
assert.ok(actual.blocks.some((block) => block.id === "intro" && block.kind === "markdownBlock"));
assert.ok(actual.datasets.some((dataset) => dataset.dataSourceRef === "steward-orders"));

const scriptSentinel = "__materializerMetadataWasExecuted";
delete globalThis[scriptSentinel];
const metadataWithScript = structuredClone(sample);
metadataWithScript.builderDefinition.reportBuilder.metadataScript = `globalThis.${scriptSentinel} = true`;
globalThis.ForgeAuthoredLowerer.lower(JSON.stringify(metadataWithScript));
assert.equal(globalThis[scriptSentinel], undefined, "authored metadata is data and must never be evaluated");

const stewardEnvelope = resolvedCases.find(([name]) => name !== "authored-envelope.json")[1];
const stewardWithScript = structuredClone(stewardEnvelope);
stewardWithScript.builderDefinition.reportBuilder.metadataScript = `globalThis.${scriptSentinel} = true`;
const stewardSpec = JSON.parse(globalThis.ForgeAuthoredLowerer.lower(JSON.stringify(stewardWithScript)));
const stewardDatasets = buildFixtureDatasets(stewardSpec);
globalThis.ForgeAuthoredLowerer.materialize(JSON.stringify({ reportSpec: stewardSpec, datasets: stewardDatasets }));
assert.equal(globalThis[scriptSentinel], undefined, "Steward hook/script metadata must remain inert through fill and print materialization");

assert.throws(() => globalThis.ForgeAuthoredLowerer.lower(JSON.stringify({ ...sample, format: "forge.nativeReport" })));
assert.throws(() => globalThis.ForgeAuthoredLowerer.lower(JSON.stringify({ ...sample, dataSources: [] })));

console.log("authored report materializer bundle ✓ trusted envelopes lower through the official report builder model");
