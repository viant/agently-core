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
`workspace-primary` typography family, and logical numeric dimensions. The
semantic name identifies a workspace's primary typeface without constraining it
to sans-serif, serif, or another classification. Workspaces own the registered
WOFF2 faces and license; the style service validates and serves those assets
through authenticated, content-addressed, same-origin URLs. Generated
application and Forge variables share the same workspace-defined family stack.
Bounds: font size 8–72; minimum control
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
