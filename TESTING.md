# Testing AII OS

This source tree includes the unit, integration, migration and protocol tests,
their synthetic fixtures, shared SDK vectors and the scripts used to qualify
the source. Run commands from the repository root with Go 1.27.1 and Git
installed. Tests use temporary stores; do not point them at a running identity.

## Local checks

```sh
go build ./...
go vet ./...
go test -race -count=1 -timeout=45m ./...
```

The timeout is per package. Store, application and plugin integration tests can
take many minutes under the race detector. On Unix, the owned-process runner
below also cleans up subprocesses from its own run if interrupted:

```sh
AII_GO="$(command -v go)" ./test/run_race_scope.sh reference
```

Tree guards run quickly before a commit:

```sh
AII_GO="$(command -v go)" ./test/guards.sh
```

## Full source gate and SDK compatibility

Clone the public SDK repository and provide its path. The compatibility script
reads exactly the SDK commit in `test/sdk-compat.rev`, without modifying that
checkout. Fetch that revision if it is not present. Install TinyGo 0.42.0 and
the native development prerequisites for your platform before running the gate.
The gate includes formatting, build, vet, cross-platform static analysis,
SDK compatibility, packaging and the complete sharded race scope. Its scope
checks that every discovered top-level test is assigned exactly once.

```sh
export AII_GO="$(command -v go)"
export AII_SDK_DIR=/absolute/path/to/aii-plugin-sdk
export AII_PLUGIN_SDK_DIR="$AII_SDK_DIR"
export TINYGO="$(command -v tinygo)"
EVIDENCE_DIR="$(mktemp -d)" ./test/gate.sh
EVIDENCE_DIR="$(mktemp -d)" GATE_ONLY=suite ./test/gate.sh
```

`AII_PLUGIN_SDK_DIR` enables the application-level voice integration tests against
the same SDK; their binaries and stores are built in temporary directories.

The second command runs the unsharded, fully instrumented reference race suite
on the same source. Evidence directories must be empty and outside the source
tree. The gate records its commit, tree, stage exits and log checksums. A complete
gate takes a host-wide lock; do not bypass it to compete with another run.

## Optional and platform-specific checks

Go selects platform-specific tests for the current operating system. Native
macOS and Windows checks require those systems; cross-compilation is not native
qualification. Browser checks require their documented browser binaries, and
Linux containment tests require a working sandbox such as bubblewrap. Read the
skip reasons and `test/release_critical.txt` before treating a run as release
evidence. A skipped test is not a passing test.

Tests that need provider credentials, real authored plugin packages, audio
fixtures, or a developer's private-term policy are explicitly opt-in. Their
environment requirements appear in the individual tests. The ordinary suite
does not establish live provider, hardware, signed installer, or identity task
performance. Never use production credentials or identity data as public test
fixtures. The committed fixtures are synthetic except for public protocol and
historical public seed documents retained for exact-byte compatibility checks.
