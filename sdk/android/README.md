## Android SDK extraction

This directory is the new home for the Agently-specific Android adapter layer.

Purpose:
- keep Forge generic
- keep Agently API/client/stream/auth logic owned by `agently-core`
- let Android apps compose `agently-core` SDK code with the generic Forge runtime

Current status:
- the Kotlin sources here are extracted from `forge/android/sdk/.../forgeandroid/agently`
- the compiled SDK currently owns the Agently client, auth, upload, and stream/tracker layer
- it depends on generic Forge runtime network types such as `EndpointRegistry`, `EndpointConfig`, and `RestClient`
- the Forge-side Agently package has been removed; Forge now stays generic
- product-specific Android runtime wiring was intentionally not kept here yet if it required Forge internals

Planned follow-up:
1. wire an Android build module around this directory
2. switch app imports to this package
3. move app/runtime customization into Agently-owned modules using public Forge extension points
