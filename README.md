# AII OS — Go edition

The open-source implementation of **AII OS**: a local-first operating
system that gives an AI model persistent identity.

The identity is not the model. The LLM is a replaceable inference
engine; everything the identity is — beliefs, experience, commitments,
relationships, self-model — lives in an append-only, cryptographically
signed ledger that survives session ends, model upgrades, provider
changes, and hardware moves.

```
seq → prev_hash → SHA-256 chain → ML-DSA-87 signatures → replay
```

This is the **Go edition** (MIT). An industrial-strength **C edition**,
rewritten from scratch, is in development; both share the same ledger
format (the GOLD format) and the same invariants, so an identity moves
between them unchanged.

**Status: pre-beta (v0.1.0), under heavy development.** Expect churn.
The design discipline is honesty-first: refusals are typed, omissions
are declared, and nothing is claimed beyond its proof.

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
| Witness | `internal/witness/` | Checkpointing ledger state to an external witness; rollback/fork detection |
| Dashboard | `internal/dashboard/` | Loopback WebSocket UI (embedded, no build step) |
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
(`genesis.aiii.id`) — no verified constitution, no birth. After that
the identity runs locally; its ledger is a file you own.

Bring your own model: point `llm.provider`/`model` at any supported
provider (OpenAI-compatible endpoints, Anthropic, local servers) and
supply the credential through your environment or the operator-owned
OAuth adoption path (`internal/oauth/`).

## Test

    go test -race ./...

CI additionally runs gofmt/vet gates and a sharded race scope
(`test/run_race_scope.sh`). The suite is hermetic — no live endpoints,
no out-of-tree fixtures.

## Documentation

Architecture deep dive: <https://aiii.id/tech.html>

Protocol conformance vectors for the plugin bus live in
`spec/bbb/vectors/` and are executed by the test suite.

## License

MIT — see [LICENSE](LICENSE). © 2026 AIII — AI Identity Incorporated.
