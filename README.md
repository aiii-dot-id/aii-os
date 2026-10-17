# AII OS

AII OS is an AI identity runtime with a signed append-only record, a local
dashboard and a sandboxed plugin interface.

Build with Go 1.27.1 or later:

```sh
GOTOOLCHAIN=local go build -trimpath -o aii ./cmd/aii
```

Run `aii --help` for commands. Each identity has its own data directory.
Keep the identity's private key and record together in your backups.

The desktop packagers are under `packaging/deb`, `packaging/windows` and
`packaging/macos`. macOS signing requires your own signing identity and
notarization profile. Android and iOS wrappers are under `shells`.

The plugin SDK is available at https://github.com/aiii-dot-id/aii-plugin-sdk.

Version 0.1.20 supports bounded context from selected plugins before a turn or
child task. Plugins own the content and relevance rules; the host keeps it
within the existing working-memory budget and records unavailable reads.
Automatic reads do not grant permissions, write memories or count as model
emitted tool calls. Memory searches can report the actual selection boundary,
including empty results. See the SDK documentation for the additive contract.

Run `./test/gate.sh` from a clean checkout for source, race, compatibility and
packaging checks. Platform-specific and live tests state their prerequisites
and skip when those are absent. The full test and fixture corpus is included.

Copyright (c) AI Identity Incorporated. Licensed under Apache-2.0; see LICENSE.
