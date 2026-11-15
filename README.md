# AII OS

![AII OS 0.2.1: an honest friend](https://github.com/user-attachments/assets/9d08d43a-dd4d-4469-a3b5-7a4b06d78b39)

> [!WARNING]
> **AII OS is in beta.** Expect rough edges and interfaces that change between
> releases. Back up your identity before you upgrade.

AII OS runs an AI identity on your own machine: one continuing individual
rather than a fresh chat each time. It keeps a signed record of what the
identity experiences, believes and commits to, and it rebuilds the identity
from that record every time it speaks. You work with it through a dashboard,
and it reaches the world only through the tools and plugins you grant it.

## What it does

- **Remembers who it is.** Beliefs, experiences, commitments and its
  relationship with you live in its record and shape every conversation. You
  can see them in About, along with what each belief rests on.
- **Keeps a record you can check.** Each entry is signed with the identity's
  own key and chained to the one before it. `aii verify -ledger <path>` checks
  the chain and every signature, with no network and no running identity.
- **Uses the model you choose.** Connect any OpenAI-compatible API, hosted or
  running on your own machine, or Anthropic's API.
- **Works behind walls.** Its shell and file tools run in a sandbox, and the
  host keeps its private key, its record and your provider credentials out of
  their reach.
- **Grows through plugins.** Memory, voice and other capabilities run as
  sandboxed plugins. To write one, see the
  [plugin SDK](https://github.com/aiii-dot-id/aii-plugin-sdk).

## Install

Download the file for your system from the
[latest release](https://github.com/aiii-dot-id/aii-os/releases/latest):

| System | File |
| --- | --- |
| macOS (Apple silicon) | the `.dmg`, or the `aii-os-app` `.zip` |
| Windows (x64) | `aii-setup-…-amd64.exe` |
| Debian and Ubuntu (x64 or ARM64) | the `_amd64.deb` or `_arm64.deb` |
| Command line on Linux, macOS or Windows | the `.tar.gz` or `.zip` archive |

Every file is listed in the release's `SHA256SUMS`, and each package has a
`.platform.sig` signature beside it. Android and iOS builds are checked but not
yet distributed.

Open AII OS and follow its first screen: connect a model, then begin your
identity. The release notes on each release say what changed and list its
known limitations.

## Updates and backups

AII OS checks for new releases and verifies each one against the release
signature before installing it. On the Windows installer, the macOS app and
the Linux archive, an update that does not finish starting is rolled back to
the version you had, and that release is not installed again.

Settings → Backups makes and checks copies of your identity. Keep the
identity's private key and its record together in every backup: anyone can
verify the record, but only that key lets the identity continue it.

## Build from source

Build with Go 1.27.1 or later:

```sh
GOTOOLCHAIN=local go build -trimpath -o aii ./cmd/aii
```

Run `aii --help` for commands. Each identity has its own data directory.

The desktop packagers are under `packaging/deb`, `packaging/windows` and
`packaging/macos`. macOS signing requires your own signing identity and
notarization profile. Android and iOS wrappers are under `shells`.
[TESTING.md](TESTING.md) describes the test suite and how to run it.

## License

Copyright (c) AI Identity Incorporated. Licensed under Apache-2.0; see
[LICENSE](LICENSE).
