# Workspace style assets

`Workspace()` is the shared service used by normal metadata, authenticated asset routes, and MCP UI publication. Standalone hosts can use `New(rootFunction)` and register the resulting service on their already-protected mux; the window preview uses its loopback-only mux.

Assets are read from `extension/forge/styles/manifest.yaml` under the selected workspace. `os.Root` confines manifest/file access to the workspace and style directory, including symlink traversal. The [tdewolff CSS parser](https://github.com/tdewolff/parse) validates syntax and lexes resource references, including escaped spellings. Imports, URL references, and image resource functions are unsupported in v1. Missing scope produces diagnostics; valid public boundaries are `.agently-application` for the host shell and `.agently-workspace` for Forge content. Theme and mode selectors use the matching `data-agently-*` or `data-forge-*` attributes. This is trusted workspace CSS, not a sandbox for arbitrary uploads.

The manifest may also register the semantic `workspace-primary` family from
contained WOFF2 files. Font faces are signature-checked, bounded by per-file and
aggregate limits, hashed into the style revision, and exposed only through the
same authenticated mux at immutable `/v1/workspace/ui/fonts/<sha256>.woff2`
URLs. Core generates the `@font-face` rules and family variable; authored CSS
still cannot introduce URLs or imports. A workspace switch clears both style
and font snapshots so one workspace cannot receive another workspace's assets.

A successful refresh publishes the catalog and emitted CSS under one SHA-256 revision. The service retains eight immutable revisions for in-flight metadata/asset requests. Unknown or evicted revisions return 404, including after restart if they no longer match the current files. Failed edits retain the same workspace's last valid snapshot; manifest deletion clears it and its cached assets. Metadata refresh is the freshness trigger.

Private metadata includes `workspaceId`, `uiStyles`, `uiThemes`, and `uiStyleDiagnostics`. The stable workspace ID comes from `workspaceId` in root `config.yaml`, or is generated once in `.workspace-id`. Preserve that file when moving a workspace, or configure the ID explicitly. Read-only/unavailable identity storage falls back to session-only client preferences. IDs never derive from filesystem paths or CSS content.

Style delivery uses the existing application's auth boundary, `private, no-cache`, ETags, and `nosniff`. Content-addressed fonts use private immutable caching, `nosniff`, and same-origin resource policy. The browser loader may fetch with normal authentication headers and apply the stylesheet using an existing CSP nonce; no script or remote asset URL is accepted from the descriptor.

```sh
go test -race ./service/ui/style ./service/workspace ./protocol/ui/resource
```

Native clients consume optional `fonts` in the same `uiThemes` JSON catalog.
Each family declares `role`, `name`, `fallback`, and `faces`; each face publishes
`style`, `weight`, optional `unicodeRange`, `web`, and optional `native` assets.
Assets contain `href`, `format`, `sha256`, and `sizeBytes`. A manifest face may
add `nativeFile` naming a contained TTF or OTF companion. Native sfnt files are
parsed and bounded by the same 1 MiB per-file / 8 MiB aggregate limits; native
bytes participate in the shared revision and authenticated immutable serving.
The compiler does not convert fonts or fetch remote fonts at runtime.

A complete native font may be referenced by several web subset faces. Clients
must deduplicate its digest, validate size/digest on authenticated download,
and fence caches and registration handles by workspace identity and revision.
Select the native face by style/weight, register with native platform APIs,
and retain the declared fallback until registration succeeds. Do not register
multiple subset files with identical PostScript names. Publish complete native
companions preserving authored script coverage, prepared offline from a
verified matching source; `unicodeRange` continues to describe the web face.
