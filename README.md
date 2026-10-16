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

Copyright (c) AI Identity Incorporated. Licensed under Apache-2.0; see LICENSE.
