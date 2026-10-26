# Portable theme contract

This package resolves manifest v1 into complete semantic token maps shared by
Forge web, the Agently web application shell, Agently iOS, and Agently Android.
`Parse` rejects unknown manifest fields and invalid tokens; `Resolve` overlays
common/mode tokens on independent built-in palettes. `CSS` maps the same
resolved values to two web boundaries: `--agently-theme-*` variables on
`.agently-application` and the established `--forge-*` variables on
`.agently-workspace`. Native clients consume JSON, never CSS.

The application variables are an opt-in bridge, not an automatic restyle.
Agently keeps its incumbent `--app-*` defaults until a workspace-scoped CSS
file maps the portable values to the public shell roles. This lets existing
workspaces remain visually unchanged while one selected theme can configure
the shell and Forge surfaces without duplicating token values.

File references are only modeled here. The workspace style service owns
filesystem containment, CSS parsing, WOFF2 validation, delivery,
authentication, and caching. Do not serve a manifest's file list without that
validation.

The canonical fixture is `testdata/baseline.yaml`. Regenerate JSON and CSS from this directory with:

```sh
go generate
go test ./...
```

The Swift and Kotlin theme tests read the same JSON in the sibling checkout. Forge's `npm run test:workspace-theme` uses the generated CSS and actual widget runtime in a browser. It expects sibling `forge`, `agently`, and `agently-core` directories. This is an integration fixture, not a runtime filesystem dependency for native clients.

Version 1 uses literal hex colors, the `system` or semantic
`workspace-primary` / `workspace-secondary` typography family, and logical numeric dimensions. The
semantic roles identify a workspace's primary and optional secondary typefaces without constraining either
to sans-serif, serif, or another classification. Workspaces own the registered
WOFF2 faces and license; the style service validates and serves those assets
through authenticated, content-addressed, same-origin URLs. Generated
application and Forge variables share the same workspace-defined family stack.
Bounds: font size 8–96; minimum control
height 16–128; radius and inline padding 0–64. Native adapters retain platform
touch-target minimums and text scaling and fall back to their system family when
a workspace family asset is unavailable. Changing these rules or default palettes
requires updating the versioned shared contract and cross-client fixtures/tests
together.

Example workspace font registration:

```yaml
fonts:
  - role: workspace-primary
    name: Example Serif Variable
    fallback: serif
    faces:
      - file: fonts/example-latin.woff2
        style: normal
        weight: '100 700'
        unicodeRange: U+0000-00FF
```

Only contained regular WOFF2 files are accepted. Family names, roles, styles,
weights, fallbacks, and optional Unicode ranges are finite validated values;
arbitrary URLs and CSS fragments are not part of this contract.

A secondary registration uses `role: workspace-secondary` and publishes
`--agently-font-workspace-secondary`. It does not change the primary family or
any theme token. A bounded workspace surface may consume that variable directly;
`typography.family: workspace-secondary` explicitly selects it for a whole theme.
Both roles must be registered before theme tokens can select them, and duplicate
roles remain invalid. Native clients use the same published role metadata and
retain system fallback when a family is unavailable.
## Complete optional typography roles (UX0)

The v1 catalog accepts `typography.<role>.family`, `.size`, `.weight`,
`.lineHeight`, and `.letterSpacing` for caption, small, body, section, heading,
title, metric, display, code, label, and metadata. Existing names and size/leading
keys are preserved. Label and metadata supply missing meanings; they are not new
size steps. Size and line height are logical pixels. Weight is an integer from
100 through 900, emitted without a unit. Letter spacing is em, bounded to
-0.04 through 0.2. Family uses the existing finite font registry plus intrinsic
`monospace`. Every workspace family used by a role must be registered. CSS, URLs,
and arbitrary family strings are rejected.

All properties remain optional. `Defaults` does not inject role values or change
legacy palettes. The web renderer owns centralized neutral fallbacks. Workspaces
supply their reviewed values. The canonical fixture now exercises all five
properties for every role in both modes. CSS emits the same values under the
application and renderer boundaries. HTML heading level remains independent.
Native validators accept these values; this does not migrate native typography
or install workspace fonts on native devices.

The same contract adds optional `spacing.1` through `spacing.6` (0 to 64),
`border.width` (0 to 4), `radius.surface` (0 to 32), `radius.pill` (0 to 999),
`focus.width` (1 to 4), `focus.offset` (0 to 8), `icon.size` (12 to 32), and
`motion.fast` (0 to 500 milliseconds). All other dimensions emit pixels. Existing
control geometry, semantic color roles and scoped CSS assets remain authoritative.
Elevation continues through the existing Forge overlay-shadow and application
shadow hooks rather than accepting arbitrary CSS in a portable token.

Generate a workspace contract without changing the canonical fixture:

```sh
go run ./protocol/ui/theme/cmd/generate -manifest /path/to/styles/manifest.yaml -output /tmp/workspace-theme
```

`service/ui/style` also offers `TestWorkspaceIntegration`. Set
`AGENTLY_STYLE_WORKSPACE` to a workspace root to compile its actual manifest,
scoped CSS and WOFF2 assets through the real service. Optional
`AGENTLY_STYLE_PROOF_OUTPUT` exports those local test bytes for Agently's browser
proof. This is a test input, not a runtime product dependency or public server.

## AI contributor entry points

The team uses Codex, Claude Code and Cursor. Root `AGENTS.md` links this guide;
`CLAUDE.md` imports that entry point, and `.cursor/rules/design-system.mdc` is an
always-applied thin pointer to this same guide. These files are versioned so a
fresh checkout has the usage path. Instructions guide contributors; the contract
and component checks reject defined violations. Neither replaces design review.

Mechanisms verified against [Claude Code project memory](https://code.claude.com/docs/en/memory)
and [Cursor project rules](https://cursor.com/docs/rules). The Cursor rule uses
`.mdc` frontmatter and `alwaysApply: true`; no user/team settings are changed.

The complete role ranges retain existing size 8–96 and line-height 8–144 bounds.
Current upstream color variable spellings remain emitted alongside compatibility
aliases, and workspace-secondary remains supported. The Core design-system CI
runs both theme and style-service suites with neutral fixtures. A real workspace
proof can set AGENTLY_STYLE_BASELINE_WORKSPACE to a separately checked-out upstream
workspace and reject new diagnostics while documenting pre-existing warnings.
GitHub requires an administrator to make the new check merge-required.
