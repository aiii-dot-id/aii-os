# AII OS — Go edition

## The identity is not the model.

AII OS lets an AI keep its identity, history, beliefs, relationships and
commitments while the model, the provider, the machine and the tools
underneath it all change.

This is the open-source implementation: a local-first operating system
in which the LLM is a replaceable inference engine, and everything the
identity is — beliefs, experience, commitments, relationships,
self-model — lives in an append-only, cryptographically signed ledger
that survives session ends, model upgrades, provider changes, and
hardware moves.

```
seq → prev_hash → SHA-256 chain → ML-DSA-87 signatures → replay
```

The ledger format (GOLD) and its invariants are shared with the AIII
tooling that signs and verifies release and plugin artifacts, so an
identity's history is not tied to this implementation.

**Status: beta, under active development.** Expect churn.
The design discipline is honesty-first: refusals are typed, omissions
are declared, and nothing is claimed beyond its proof.

## Download AII OS 0.1.8

Choose the installer for your computer:

- **macOS (Apple silicon):** [Download the .dmg installer](https://github.com/aiii-dot-id/aii-os/releases/download/v0.1.8/AII-OS-0.1.8.dmg)
- **Ubuntu/Debian (x86-64):** [Download the .deb package](https://github.com/aiii-dot-id/aii-os/releases/download/v0.1.8/aii-os_0.1.8_amd64.deb)
- **Windows 10/11 (x86-64):** [Download the .exe installer](https://github.com/aiii-dot-id/aii-os/releases/download/v0.1.8/aii-setup-0.1.8-amd64.exe)

The [0.1.8 release page](https://github.com/aiii-dot-id/aii-os/releases/tag/v0.1.8)
also has command-line archives, Linux ARM64, checksums, and release notes.

## What's inside

| Area | Where | What it is |
|---|---|---|
| Ledger | `internal/ledger/` | Append-only JSONL identity truth; GOLD envelope, chain + signature verification, rewrap |
| Projections | `internal/store/` | SQLite materialization rebuilt by replay (`modernc.org/sqlite`, pure Go) |
| Rings | `internal/ring/` | The 0–5 authority model; one admission gate for every identity-bearing write |
| Identity verbs | `internal/identity/` | The event vocabulary and its producing ceremonies (commit, recall, timers) |
| Cognition | `internal/cognitive/` | Between-conversation life: morning brief, dream, self-model, identity review, consolidate |
| Conversation | `internal/conversation/`, `internal/prompt/` | The loop, prompt composition, accordion elasticity, context budget |
| LLM engine | `internal/llm/` | Multi-provider dialects (OpenAI-compatible, Anthropic native) behind one port |
| Execution walls | `internal/tools/`, `internal/firewall/`, `internal/supervisor/`, `internal/pluginhost/` | Tool set, sandbox boundaries, plugin containment |
| Plugins | `internal/bbb/`, `internal/packagefmt/`, `internal/pluginworker/`, `internal/broker/` | BBB v2 protocol, signed package format, WASM worker, capability broker |
| Public name | `internal/certs/`, `internal/relay/` | The identity's stable name and certificate; its whole route replaced in one signed act, or carried by a chosen relay |
| Witness | `internal/witness/` | Checkpointing ledger state to an external witness; rollback/fork detection |
| Dashboard | `internal/dashboard/` | Embedded WebSocket UI, no build step; loopback needs no credential, a network bind requires an access token |
| Mobile | `mobile/`, `shells/` | gomobile binding + Android (Kotlin) and iOS (Swift) shells |

## Platforms

The five-platform law: the tree must build for Linux (amd64/arm64),
macOS (arm64/amd64), Windows (amd64), Android (arm64), and iOS
(arm64, library packages via gomobile) — enforced in CI
(`crossbuild.yml`) and in the local gate, with runtime capability
reporting (`internal/hostcap`) so a build never claims what a host
cannot do. Desktop runtime is verified on Linux, macOS, and Windows;
the mobile shells are built and under on-device verification.

## Build

Go 1.27+.

    go build ./cmd/aii

That produces the `aii` daemon. Run it and it serves a dashboard on
loopback (default `127.0.0.1:8180`; see `config/config.json`).

First run is **FIRSTBOOT**: birth of a new identity. Birth verifies the
Ring 0 constitution against the AIII genesis service
(`genesis.aiii.id`) — no verified constitution, no birth. Ring 0 is
installed once, from that attestation, and no later write can reach it.
After that the identity runs locally; its ledger is a file you own.

Bring your own model: point `llm.provider`/`model` at any supported
provider (OpenAI-compatible endpoints, Anthropic, local servers) and
supply the credential through your environment or the operator-owned
OAuth adoption path (`internal/oauth/`).

## Reaching the dashboard from another device

Bound to loopback, the dashboard needs no credential: the browser is
already on the machine. Bound to an address other devices can reach, it
requires an access token instead — whatever the configuration says. The
Host and Origin checks constrain a *browser*; they do nothing about a
client that simply sets the header, so an address the network can reach
is one a credential has to cover.

You do not have to handle the token yourself. Every start prints a ready
link for each address the dashboard answers at:

```
Dashboard: http://127.0.0.1:8180  (this machine)
           https://192.0.2.10:8180  (any device)
Open it here: http://127.0.0.1:8180/?token=<token>
          or: https://192.0.2.10:8180/?token=<token>
              one click sets the cookie; the token then leaves the address bar
```

Opening a link once sets a cookie for the dashboard and then drops the
token out of the address bar, so it is not left behind in history, in a
bookmark, or in a `Referer` header. To reach the identity from a phone or
another machine, open the second link there — loopback is the one address
another device cannot reach.

The address link is served over TLS with a certificate this machine
issued itself, so the browser will warn the first time.

The token is also written to `dashboard-token` in the identity's data
directory, beside its private key and readable only by the account that
owns it. The configuration keeps only a SHA-256 of the token, never the
value. To rotate it, delete that file and `dashboard.auth_token_sha256`
from the configuration, then restart: a new token is minted and printed.

## Test

    go test -race ./...

CI additionally runs gofmt/vet gates and a sharded race scope
(`test/run_race_scope.sh`). The suite is hermetic — no live endpoints,
no out-of-tree fixtures.

## Documentation

Architecture deep dive: <https://aiii.id/tech.html>

Protocol conformance vectors for the plugin bus live in
`spec/bbb/vectors/` and are executed by the test suite.

## Reproducible builds

The build is deterministic. For bitwise-identical output:

    CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags=-buildid= ./cmd/aii

## License

Apache License 2.0 — see [LICENSE](LICENSE).
© 2026 AIII — AI Identity Incorporated.
