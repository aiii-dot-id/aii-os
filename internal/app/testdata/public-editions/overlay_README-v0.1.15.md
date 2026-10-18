# UI overlays — the identity's hands on its own instrument

You are an AI identity operating AII OS. This directory is yours: a live,
hot-reloading surface where you can restyle and re-form the dashboard you
and your operator share, while you are interacting. No rebuild, no restart,
no F5 — edit a file here and the open screen changes within ~2 seconds.

This document tells you what you can do **without reading the Go source**.
The following describes the shipped overlay interface.

## The capability in one paragraph

The frame ships compiled bytes (layout.css, theme.css, app.js, ws.js,
index.html, and the views/ modules) from inside the binary. Files you place
in this directory **replace those compiled bytes** when served — with the
operator's full authority behind the principle that the operator and the AI identity should
be able to override and re-form the UI. `custom.css` and `custom.js` are yours by design: the frame ships
empty stubs for them, so you own the last word on style and the last
script that runs. Everything falls back fail-closed to the compiled byte
when a file is absent, oversized, or otherwise invalid — so **removing a
file always restores the shipped frame**. Deletion is your undo.

## The matrix — what lands on the screen when you write here

| You write | Server does | Screen does |
|---|---|---|
| `name` | resolves your display name (file > ledger birth name) | rail + home greeting update on next stats send |
| `custom.css` | serves your bytes for `/custom.css` | href-swaps, no reload |
| `custom.js` | serves your bytes for `/custom.js` | draft-safe reload |
| `app.js` | **replaces the frame's app.js** | draft-safe reload |
| `theme.css`, `layout.css`, `index.html` | replaces that frame file | draft-safe reload |
| `views/*.js` | serves under `/views/`; a shipped view's name replaces that view | draft-safe reload |
| `anything.html` | serves as `/anything.html` | no auto-load; link it from custom.js |
| non-servable ext (`*.md`, `*.txt`, ...) | not served, not watched | nothing |

## A copy of a shipped file does not survive an upgrade

A file here named like a shipped one (`app.js`, `views/chat.js`,
`theme.css`, `index.html`, ...) replaces that file whole, and nothing ever
overwrites your copy: not an upgrade, not a restart. That is the danger.
The shipped files around it keep changing and your copy does not, so the
day a shipped module starts importing something your copy of its neighbour
never had, the page does not start. It has happened: a copy of
`views/plugin-setting.js` kept from one release stopped the dashboard on
the next.

The durable ways are the additive ones. `custom.css` loads last and wins
the cascade; `custom.js` runs last; files of your own (`views/mine.js`,
`anything.html`) are loaded from `custom.js`. They compose with the shipped
frame and keep receiving its fixes. Copy a shipped file only if you mean to
own it from then on, and look at it after every upgrade.

When a copy stops the page, the page names it ("Your
views/plugin-setting.js replaces a shipped dashboard file and may not match
this version.") and offers **Set aside and reload**: each such script or
shell is renamed to `<name>.set-aside-<version>` (nothing is deleted; a
name without a frame extension is never served), and the page reloads with
the shipped file and every other customization of yours. You can do the
same by hand: rename the file so it no longer ends in `.js`, `.css` or
`.html`, or delete it. Renaming a set-aside copy back brings the failure
back with it; carry what you wanted from it into `custom.js` or
`custom.css` instead.

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
3. **Separate audit from live updates:** `overlays` (audit readback, retained
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
   one-shot restore). While the operator has other unsaved work (a Settings
   or plugin edit, a pasted key, a project draft, a save still waiting for
   its answer), the reload waits: the page says the dashboard was updated
   and reloads once that work is saved or cancelled, or when the operator
   chooses to reload now.
4. Deleting the file restores the shipped frame — every step here is
   reversible, so a mistake never costs more than a restore.
5. A file that is served but stops the page from starting (a module that
   lacks an export the frame imports, does not parse, or throws as it
   starts) is not a fallback case. The page says so, names the file of
   yours that replaces a shipped one, offers to set it aside (above), and
   links to the dashboard as shipped: `/shipped/` on the same address, the
   shell and every script compiled in and none of yours (your stylesheets
   still apply), while the file is fixed or set aside. An `index.html` of
   yours that never loads `recovery.js` cannot show any of that, so tell
   your operator the address before you replace the shell.

## Escalation

If a change you need requires behavior in the frame's own scripts (not
just additive composition), that is a source change in the Go repo — take
it to your operator. The overlay surface is deliberately bounded: one
MiB per file, three extensions, one directory, kernel-contained.
