import assert from "node:assert/strict";
import { mkdir, readFile, readdir, writeFile } from "node:fs/promises";

import "../lowerer.bundle.js";
import { buildReportFillFromReportSpec } from "../../../../../forge/src/reporting/reportFillModel.js";
import { buildReportPrintFromReportFill } from "../../../../../forge/src/reporting/reportPrintModel.js";
import { buildFixtureDatasets, freezeFixtureDate, rawJSONFingerprint, stableFingerprint } from "./materialization-fixtures.mjs";

freezeFixtureDate();

const fixtureDir = new URL("../testdata/steward/", import.meta.url);
const outputDir = new URL("../testdata/steward_materialization/", import.meta.url);
await mkdir(outputDir, { recursive: true });
const manifest = {};
for (const file of (await readdir(fixtureDir)).filter((name) => name.endsWith(".json")).sort()) {
  const envelope = JSON.parse(await readFile(new URL(`../testdata/steward/${file}`, import.meta.url), "utf8"));
  const reportSpec = JSON.parse(globalThis.ForgeAuthoredLowerer.lower(JSON.stringify(envelope)));
  const datasets = buildFixtureDatasets(reportSpec);
  const reportFill = buildReportFillFromReportSpec(reportSpec, datasets);
  const reportPrint = buildReportPrintFromReportFill({ reportSpec, reportFill });
  const goja = JSON.parse(globalThis.ForgeAuthoredLowerer.materialize(JSON.stringify({ reportSpec, datasets })));
  assert.deepEqual(goja, { reportFill, reportPrint }, `${file} bundled materialization parity`);
  await writeFile(new URL(`${file}`, outputDir), `${JSON.stringify(datasets)}\n`);
  manifest[file] = {
    reportSpec: stableFingerprint(reportSpec),
    reportFill: stableFingerprint(reportFill),
    reportPrint: rawJSONFingerprint(reportPrint),
  };
}
await writeFile(new URL("expected.json", outputDir), `${JSON.stringify(manifest, null, 2)}\n`);
console.log(`authored report materialization fixtures ✓ ${Object.keys(manifest).length} Steward envelopes`);
