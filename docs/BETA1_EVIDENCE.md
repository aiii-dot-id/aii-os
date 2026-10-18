# Release journey evidence template

This is an unexecuted public template, not a release qualification receipt.
For each platform, record the exact source and artifact hashes, command, result,
skips, and evidence location. A source build does not establish an installed
journey. Use disposable accounts for destructive installation and removal tests.

| Journey | Behavior | Result | Evidence |
|---|---|---|---|
| 1 | Install and start AII OS | NOT RUN | — |
| 2 | Create or open a project | NOT RUN | — |
| 3 | Birth or resume a resident without continuity ambiguity | NOT RUN | — |
| 4 | Configure a local or remote model | NOT RUN | — |
| 5 | Converse, execute authorized tools, cancel work, recover from provider failure | NOT RUN | — |
| 6 | Stop and restart without ledger or project loss (`drills/recovery.sh`) | NOT RUN | — |
| 7 | See honest SAFE, degraded, disconnected, and recovery states (`drills/safe.sh`) | NOT RUN | — |
| 8 | Verify, back up, and restore identity history (`drills/safe.sh`) | NOT RUN | — |
| 9 | Apply a signed update and survive rollback (`drills/update.sh`, `drills/rollback.sh`; macOS: failing signed candidate) | NOT RUN | — |
| 10 | Uninstall without destroying retained identity and project data (`drills/uninstall.sh`, `drills/uninstall_account.sh` on disposable accounts) | NOT RUN | — |
