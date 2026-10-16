# Host runtime composition

This package assembles operator-owned authorization and resource configuration for a workspace host. It prepares provider factories, revision mappings, navigation and hosted-app readers, immutable authored sources, native window authority, and window delivery. It depends on `app/executor` and the source/provider helpers in `app/host`; those packages do not depend on this composition package.

The embedding application supplies `Options` and its linked UI artifact through `Options.LinkedMetadata`. Framework source constructors and `DefaultFrameworkWindows` also take that artifact explicitly. Core does not import an application's metadata package. A workspace directory cannot replace linked framework definitions.

Call `ConfigureNativeWindows` before constructing the executor, use the builder callback returned by `ConfigureAuthorization`, then call `BindNativeWindowRuntime` and `Registration.Bind` with the constructed runtime before serving requests. `Registration.MenuReader` and `Registration.HostedAppReader` expose only configured readers to the application's HTTP wiring.

`app/host/reporting` separately owns reporting discovery, development reloads and window/view enrichment. This package uses its registry enrichment when compiling immutable source snapshots. Managing MCP providers retain resource and datasource authority; host composition does not add business ACL grants.

Tests use a frozen linked chat/schedule artifact under `internal/testmetadata`, including platform variants and the shared file-browser dependency. The application continues to supply and test its own current linked artifact.
