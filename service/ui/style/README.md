# Workspace style assets

`Workspace()` is the shared service used by normal metadata, authenticated asset routes, and MCP UI publication. Standalone hosts can use `New(rootFunction)` and register the resulting service on their already-protected mux; the window preview uses its loopback-only mux.

Assets are read from `extension/forge/styles/manifest.yaml` under the selected workspace. `os.Root` confines manifest/file access to the workspace and style directory, including symlink traversal. The [tdewolff CSS parser](https://github.com/tdewolff/parse) validates syntax and lexes resource references, including escaped spellings. Imports, URL references, and image resource functions are unsupported in v1. Missing scope produces diagnostics; this is trusted workspace CSS, not a sandbox for arbitrary uploads.

A successful refresh publishes the catalog and emitted CSS under one SHA-256 revision. The service retains eight immutable revisions for in-flight metadata/asset requests. Unknown or evicted revisions return 404, including after restart if they no longer match the current files. Failed edits retain the same workspace's last valid snapshot; manifest deletion clears it and its cached assets. Metadata refresh is the freshness trigger.

Private metadata includes `workspaceId`, `uiStyles`, `uiThemes`, and `uiStyleDiagnostics`. The stable workspace ID comes from `workspaceId` in root `config.yaml`, or is generated once in `.workspace-id`. Preserve that file when moving a workspace, or configure the ID explicitly. Read-only/unavailable identity storage falls back to session-only client preferences. IDs never derive from filesystem paths or CSS content.

Style delivery uses the existing application's auth boundary, `private, no-cache`, ETags, and `nosniff`. The browser loader may fetch with normal authentication headers and apply the stylesheet using an existing CSP nonce; no script or remote asset URL is accepted from the descriptor.

```sh
go test -race ./service/ui/style ./service/workspace ./protocol/ui/resource
```
