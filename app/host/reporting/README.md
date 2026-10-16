# Workspace reporting registry

This package loads a workspace's Forge reporting registry, optionally watches development assets, and enriches report windows and view catalogs from the current valid registry. Invalid reloads retain the last valid registry.

`Configure` returns a `Runtime`; the host must call `Close` during shutdown. Use `Runtime.WindowEnricher` when loading workspace windows and `Runtime.EnrichView` when listing views. `RegistryEnricher` also accepts an explicit registry callback for immutable source compilation.

The package depends on Forge's reporting registry, Core view contracts and Core window loading. It does not depend on host runtime composition, an embedding application's metadata, or a particular report datasource implementation.
