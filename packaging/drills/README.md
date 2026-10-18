# Beta drills

Each drill drives the real `aii` binary with its real verbs, reads the
real log from a mark taken before the action, and appends one evidence
line to `$EVIDENCE` (default `~/.aii/beta-evidence.tsv`) only after the
observation that justifies it. Run them on a journey host, as the user
that owns the install, with a slot that has run long enough to have a
backup. `sh <drill> --help` prints what it does and touches nothing.

| Drill | Journey | Mechanism | Evidence when it passes |
|---|---|---|---|
| `safe.sh` | 7, 8 | appends a non-record to the ledger's tail; boots; moves the live database aside and restores the pair — `aii.db` and the tail — from the newest snapshot that verifies (`data/backups/ledger-<utc>-seq<N>/`, its `SHA256SUMS` and its chain) | `SAFE MODE: entering` after the damage; a healthy boot with no SAFE after the restore; the displaced database kept, never deleted |
| `recovery.sh` | 6 | SIGKILLs the live process; boots again | `aii verify -ledger` passes; no records lost; project manifests unchanged |
| `rollback.sh` | 9 (second half) | plants the updater's own post-swap state with `attempts=1` and no marker | `rolled back to previous binary`; the binary on disk hashes as the backup; tombstone gone; healthy boot. **macOS: SKIP** — bundles roll back through the bundle seam, exercised by journey 9 with a deliberately failing signed candidate |
| `update.sh` | 9 (first half) | waits for the operator-triggered swap, or the package manager's install on a package-managed host | the build stamp changes and a fresh boot carries it; backup and tombstone present where the self-updater swapped |
| `uninstall.sh` | 10 | `aii unregister` | service gone; ledger hash unchanged; manifests unchanged; ledger verifies; re-registered after |
| `uninstall_account.sh` | 10 (whole) | `aii uninstall -yes -json`, on a **throwaway account only** (refused unless `AII_DRILL_THROWAWAY=yes`) | every slot's and the machine service's label, unit or Run value gone; the service directory and the default store gone or named in the report; every ledger's hash and manifest inventory unchanged; every ledger verifies; `aii register` brings the machine service back |

Journeys 1–5 are human-driven (install, project, birth, model, converse)
and are recorded directly in `docs/BETA1_EVIDENCE.md` with the same
columns. A result the operator did not execute is labelled **relayed**.

Environment: `AII`, `AII_SLOT` (default `identity-0`), `AII_ROOT`
(default `~/.aii`), `EVIDENCE`.

On Windows the drills run in Git's shell (`sh.exe`, with `/usr/bin` first
on the path); the identity's process is found through WMI and ended with
`taskkill`, and the uninstall drill checks the autostart entry. Run a
drill as a file, never inline from a shell whose own command line carries
the slot's path — it would find itself.

On Windows, give `AII` as a POSIX path (`/c/Users/<you>/AppData/Local/Programs/AII OS/aii.exe`),
with Git's `/usr/bin` first on PATH; the Windows form never resolves through `command -v`. Start
`update.sh` before triggering the check: a boot checks at once, and the swap can complete within
seconds — before the drill has taken its baseline.

## Ubuntu: the package path is `dpkg -i` alone

The identity runs as a systemd user unit. Package installation handles its
service transition. Do not restart the entire user manager: that would end
unrelated desktop applications and identity processes. If an explicitly
authorized repair needs a restart, identify the affected identity's leaf unit
and use the normal service operation for that unit alone.
