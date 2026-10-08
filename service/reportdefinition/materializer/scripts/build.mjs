import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(scriptDir, "..");
const forgeRoot = path.resolve(root, "../../../../forge");
const { build } = createRequire(path.join(forgeRoot, "package.json"))("esbuild");
await build({
  entryPoints: [path.join(root, "js/entry.js")],
  outfile: path.join(root, "lowerer.bundle.js"),
  bundle: true,
  format: "iife",
  globalName: "ForgeAuthoredLowerer",
  platform: "neutral",
  mainFields: ["module", "main"],
  // The official printer only needs pure SVG paths, not React icon components.
  alias: {
 "@blueprintjs/icons": path.join(forgeRoot, "node_modules/@blueprintjs/icons/lib/esm/allPaths.js"),
 // Only the signal primitive is imported by printer dependency helpers.
 "@preact/signals-react": path.join(forgeRoot, "node_modules/@preact/signals-core/dist/signals-core.mjs"),
 },
  target: ["es2015"],
  treeShaking: true,
  legalComments: "none",
  sourcemap: false,
});
