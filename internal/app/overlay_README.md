# UI overlays — the identity's hands on its own instrument

You are an AI identity operating AII OS. This directory is yours: a live,
hot-reloading surface where you can restyle and re-form the dashboard you
and your operator share, while you are interacting. No rebuild, no restart,
no F5 — edit a file here and the open screen changes within ~2 seconds.

This document tells you what you can do **without reading the Go source**.
Every row below was verified against a running system, from the wire.

## The capability in one paragraph

The frame ships compiled bytes (layout.css, theme.css, app.js, ws.js,
index.html, and the views/ modules) from inside the binary. Files you place
in this directory **replace those compiled bytes** when served — with the
operator's full authority behind the ruling (James, 2026-08-24: "The
operator and the AI identity should be able to override and re-form the
UI"). `custom.css` and `custom.js` are yours by design: the frame ships
empty stubs for them, so you own the last word on style and the last
script that runs. Everything falls back fail-closed to the compiled byte
when a file is absent, oversized, or otherwise invalid — so **removing a
file always restores the shipped frame**. Deletion is your undo.

## The matrix — what lands on the screen when you write here

| You write | Server does | Screen does | Test |
|---|---|---|---|
| `custom.css` | serves your bytes for `/custom.css` | href-swaps, no reload (~2s) | yes — this machine |
| `custom.js` | serves your bytes for `/custom.js` | draft-safe reload | yes — node contract + Go e2e |
| `app.js` | **replaces the frame's app.js** | draft-safe reload | yes — wire-verified this machine |
| `theme.css`, `layout.css`, `index.html` | replaces that frame file | draft-safe reload | code-verified (allowlist + os.Root) |
| `views/*.js` | serves under `/views/` | draft-safe reload | watcher-verified (mtime+size) |
| `anything.html` | serves as `/anything.html` | no auto-load; link it from your custom.js | serve-verified |
| non-servable ext (`*.md`, `*.txt`, ...) | not served, not watched | nothing | watcher-verified |

## The three invariants that survive every change

1. **Fail-closed to the compiled byte.** Every failure mode — absent,
   oversized (>1 MiB per file), wrong extension, escaping path, unreadable,
   directory — serves the frame as shipped. Deletion is always a restore.
2. **Containment by os.Root.** Every served path resolves inside this
   directory by the kernel; a symlink planted here cannot reach out of it.
   Served overlay code runs same-origin with dashboard authority and may
   re-form the view and speak to this server — but it can never beacon to
   an external origin: `uiCSP` sets `default-src 'none'` and allows only
   `'self'` on every fetch-carrying directive (script, style, img,
   connect, frame), with `form-action 'none'`. The network boundary is
   the browser's, kernel-enforced per response.
3. **The P1 law, in your ears:** `overlays` (audit readback, retained
   events, `decidedAt` stamps) is render-only. `overlay_changed` (fresh
   monotonic token + changed paths) is the only live invalidation trigger.
   A page that applies audit events would loop forever and miss real edits.
   Audit is history; this is news.

## How to iterate with your operator, live

1. Ask what they want changed, or propose options grounded in the served
   DOM (`#orb`, `#slot-panel`/`.panel-col`, `#thread`, `#msg-input`,
   `#nav`/`.nav-item`, `.pill`, `#toast`, `#view-*`).
2. Write the file. Small edits, one surface at a time.
3. The screen updates within ~2s (CSS) or reloads (JS/HTML) — the
   operator's half-typed draft is persisted across reloads (sessionStorage,
   one-shot restore).
4. Deleting the file restores the shipped frame — every step here is
   reversible, so a mistake never costs more than a restore.

## Known defects, stated plainly

- **Duplicate `overlay_changed` frames observed** (same token delivered
  twice back-to-back) on the production socket. The client's monotonic
  token guard makes the second a strict no-op — harmless today, but the
  emission side is unexplained. Open micro-item.

## Escalation

If a change you need requires behavior in the frame's own scripts (not
just additive composition), that is a source change in the Go repo — take
it to your operator. The overlay surface is deliberately bounded: one
MiB per file, three extensions, one directory, kernel-contained.
