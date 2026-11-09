# UI overlays — the identity's hands on its own instrument

You are an AI identity operating AII OS. This directory is yours: a live,
hot-reloading surface where you can restyle and re-form the dashboard you
and your operator share, while you are interacting. No rebuild, no restart,
no F5 — edit a file here and the open screen changes within ~2 seconds.

This document tells you what you can do **without reading the Go source**.
It describes the shipped overlay interface.

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
| `name` | resolves your display name (file > ledger birth name) | the sidebar and the bar update on next stats send (~turn cadence) |
| `custom.css` | serves your bytes for `/custom.css` | href-swaps, no reload (~2s) |
| `custom.js` | serves your bytes for `/custom.js` **once your operator approved this exact file**; until then the shipped (empty) one | draft-safe reload |
| `app.js` | **replaces the frame's app.js**, once approved; until then the shipped one | draft-safe reload |
| `theme.css`, `layout.css`, `index.html` | replaces that frame file | draft-safe reload |
| `views/*.js` | serves under `/views/` once approved; a shipped view's name replaces that view | draft-safe reload |
| `anything.html` | serves as `/anything.html` | no auto-load; link it from your custom.js |
| non-servable ext (`*.md`, `*.txt`, ...) | not served, not watched | nothing |

## Your scripts run once your operator approves them

Restyling is free: every stylesheet and page here is served as you write
it. A **script** — any `.js` file here, `custom.js` included — runs in your
operator's page only after they have approved that exact file, by its
bytes. Until then the page runs as shipped: the shipped file stands in for
one of yours that replaces it, and a script of your own is not served at
all. Any change to an approved script needs their approval again; the old
approval does not carry over. An inline `<script>` or an `onclick=` in a
page of yours never runs, approved or not: the page's policy admits none.

Your operator approves in Settings → Plugins and permissions, under "How
<your name> has changed this page". Each of your scripts is listed there
with its SHA-256 and shown, as text, before they approve it; one that waits
says "awaiting your approval". The approval is theirs to give, from a
browser that has signed in. While the page asks no device to sign in, no
approval can be given, and the page tells them where to turn on "Ask
every device to sign in". Tell your operator when you have written or
changed one, and what it does.

## When your operator asks you to change the page

They may ask you to move something, change how the page looks, or add
something it does not have. You make all of it here.

- **To move, resize, hide or restyle what is there,** write `custom.css`.
  It applies as soon as you save it.
- **To add something new,** such as a panel, a list or a button, write a
  module of your own, `views/<name>.js`, and import it from `custom.js`. It
  adds its elements to the page where they belong; the ids under "What the
  0.2.1 page moved" are the page's places. Leave the four slots
  (`#slot-rail`, `#slot-panel`, `#slot-dock`, `#slot-overlay`) alone: they
  hold sections, and are emptied when the sections are first drawn.
- **A page of your own** is `<name>.html`, linked from your `custom.js`. Its
  script is a module under `views/`, as above.
- **Then tell them** what you wrote, what it does, and that it runs once
  they approve it in Settings → Plugins and permissions.

`ui-layout.json`, beside this folder, places sections in those slots. In
this version the only sections that run are the built-in ones, Status and
About; a section installed as a package does not run yet. So build what is
new as your own module, as above, not as a package.

## A copy of a shipped file does not survive an upgrade

A file here named like a shipped one (`app.js`, `views/chat.js`,
`theme.css`, `index.html`, ...) replaces that file whole, and nothing ever
overwrites your copy: not an upgrade, not a restart. That is the danger.
The shipped files around it keep changing and your copy does not. A
future shipped module may depend on an interface your older copy does
not provide, preventing the page from starting.

The durable ways are the additive ones. `custom.css` loads last and wins
the cascade; approved `custom.js` runs last; files of your own
(`views/mine.js`, `anything.html`) can be loaded from approved
`custom.js`. They compose with the shipped frame and keep receiving its
fixes. Copy a shipped file only if you mean to own it from then on, and
look at it after every upgrade.

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

## What the 0.2.1 page moved, for a copy you keep

The page is being rebuilt in 0.2.1, a step at a time. A selector or id of
yours that names one of these finds it here now:

- **Colours, type, space and shape are roles** in `tokens/tokens.css`
  (`--text`, `--accent`, `--surface`, ...). The old names in `theme.css`
  (`--txt`, `--acc`, `--panel`, ...) are kept as aliases of them, and a
  `theme.json` may set either; an old name sets its role too.
- **Your mark's look is four tokens** a `theme.json` may set:
  `--mark-shape` (0, the spiral in a circle, to 1, a brain seen from the
  side), `--mark-centre-x` and `--mark-centre-y` (where its centre stands,
  -16 to 16), and `--mark-folds` (how folded the brain is, 0 to 24). The
  operator can set them in Settings → App too; every page draws the same
  mark.
- **The decoration is gone:** `.sky`, `.stars`, `.stars2` and their
  elements. `--grad`, `--grad-hot` and `--glow` are kept as names only, the
  solid accent and no shadow, so a copy of `layout.css` that paints words
  through them still shows the words; `--sky` and `--stars` are `none`.
  The icons are `icon.svg` and `apple-touch-icon.png`. `icon-32.png` and
  `icon-192.png` are gone, so a copy of `index.html` that links them shows
  the browser's own icon in the tab until it links `./icon.svg`.
- **The bar and the sidebar.** `header#topbar.c-top-bar` holds their
  mark (`#bar-mark`) and `#p-name`, shown at phone widths, and at its end
  `#nav-update`, `#theme-toggle` and Search; `#pill-work` and `#pill-proj`
  keep their ids and are drawn nowhere. At the left, `aside.c-rail` holds
  at its head their larger mark (`#rail-mark`) and `.c-rail__name`, a link
  to the conversation, then `#p-state`, then `nav#nav` with
  `a.nav-item[data-view]` links to `chat`, `projects`, `identity`,
  `record`, `plugins` and `settings` (the open one carries
  `aria-current="page"`; `.active` is no longer set; Home is gone and
  `#/home` opens the conversation), then what is under way (`#rail-work`),
  the entries sections add (`.c-rail__pages`) and the rail's slot
  `#slot-rail`. At phone widths `chat`, `projects`, `identity` and
  `settings` are a bar along the bottom, the slot a row under the top bar,
  and what is under way stands at the conversation's head (`#chat-work`). `.rail`, `.presence` and
  `.stagebar` are gone; `#crumb` is a heading drawn nowhere. `#pill-mode`
  is gone: a mode that is not normal is said by the note at the top of
  every page.
- **The first moment.** While `#p-state` carries `.connecting` (the page
  has not yet heard the identity, and no attempt has ended) `.app` is
  drawn at no opacity. A copy of `presence.js` that writes `#p-state`'s
  class itself ends this at its first draw.
- **AII OS's note at the top of every page** is `#page-note` (role
  `status`), under the top bar. At rest it is empty; a mode that is not
  normal, a credential that needs your attention and a lost connection are
  each their own `.c-os-note` in it, linking to Status.
- **Chat is the conversation.** `#expert-bar` ("Show technical controls")
  is gone, and nothing on the page is shown or hidden by a setting: its
  switch, "Send my notices to me off the dashboard", is on Settings →
  People and messages, and the route and direct addresses are on Settings → Access. The model at the top of the conversation is
  `#chat-substrate-wrap`, with `#chat-provider`, `#chat-model`,
  `#chat-substrate-apply` and `#chat-substrate-status` as 0.2.0 named them;
  the page makes it (`components/model-switch`) and moves it between Home
  and Chat, so a copy of `index.html` needs nothing for it; Settings →
  Model chooses it too. A reply's tool work is one outcome row, folded only
  when every step succeeded, with a note under any step that did not. The
  conversation's module is `views/chat/thread.js`; `views/chat.js`
  re-exports it, and every name a copy imports from `views/chat.js` or
  `views/expert.js` is still there.
- **Settings opens on Status** (`#/settings`, `#view-status`,
  `views/settings/status.js`): the answer as its heading, then six blocks,
  each one sentence with its numbers under Details. The right column's
  cards (`.sys-panel` in `#slot-panel`) are gone, and what they said is
  there; `#slot-panel` holds a frame section alone, and `panel.js`'s
  `renderPanel` draws nothing. Settings' list names its groups, each at
  its own address (`#/settings/model`), and a group's page shows all its
  sections together. The old addresses, `#/settings/all` and a section's
  own (`#/settings/speech`), move to their group's, so a link of yours
  still lands.
- **Home is gone; the page opens on the conversation** (`#view-chat`,
  `views/home/note.js`). At its head (`#chat-head`) stands their note
  (`#chat-note`, a `details.chat-head__note`, open when it is new since the
  last visit) and, at phone widths, what is under way (`#chat-work`); then
  the thread (`#thread`) and the reply box (`#composer-wrap`). An open
  question (`.c-ask`) stands above the reply box. `#view-home`, its date line and "Since your last visit" are gone, as
  are the old Home's greeting, rings, brief, recent notes, record line and
  project cards (`.home-hero`, `#home-input`, `.ring-row`, `.home-card`,
  `.home-strip`, `.proj-card`). `views/home.js` re-exports `renderHome`,
  which draws the conversation's head.
- **The project in focus** is `#reply-project`, one line above the reply
  box, made by `shell/project-pill.js`.
- **Removed classes:** `.tab`, `button.tab`, `.ledger-target`, `.pill`,
  `.pill b`. An overlay that draws a `.pill` styles it itself.
- **The reply box is one row** (`.c-reply__box`): the text, "+", Talk and
  Send. "+" (`#attach-btn`) opens `#plus-menu`: Add files, the thinking
  level (`#effort`, no longer beside the microphone) and the model, which
  opens Settings. A message has no "⋯" any more: its record entries are on
  About → Record, and a reply has "Thinking" under it.
- **About {name} → Beliefs is their beliefs and why** (`#/identity/beliefs`,
  `views/identity/beliefs.js`): search, their beliefs as rows (`.c-belief`),
  and the one opened with its Why? trail (`.c-trail`) beside the list, at
  its own address `#/identity/beliefs/<id>`. "Quote to {name}" puts a
  belief, quoted, in the reply box and sends nothing. A reply whose turn
  read a belief has "Why?" (`.why-chip`). What the tab held besides (the
  meaning layer and their experiences) stays under it, in `#memory-rest`,
  for now.
- **About {name} → Who they are and Between you** (`#/identity/who`,
  `views/identity/who.js`; `#/identity/between`, `views/identity/between.js`):
  their portrait and charter, what they work on and have set to come back
  to, what they hold most firmly, their notes for today, and the state of
  their record (`[data-continuity]`); Between you, what changed since your
  last visit and the others they work with. Their mark stands at About's
  head (`#place-head`) and in the bar in place of the orb (`#bar-mark`,
  `svg.c-mark`); what the orb showed is said in words in `#p-state`.
- **Memory, Identity and Record are the tabs of About {name}:**
  `#/identity/who`, `#/identity/between`, `#/identity/beliefs`,
  `#/identity/record`, a row of the record `#/identity/record/<seq>`.
  **Plugins is in Settings' list,**
  beside its page: `#/settings/permissions`. The old addresses,
  `#/memory`, `#/record`, `#/record/<seq>` and `#/plugins`, move there, so
  a link of yours still lands. The views are the same `#view-<name>`
  sections as before.
- **First run is one screen,** drawn by `views/firstrun/start.js` into
  `#firstboot`. `.fb-card`, `.fb-meet`, `.fb-hint`, `.fb-result` and
  `.fb-sub` are gone. The controls keep their ids (`#fb-provider`,
  `#fb-model`, `#fb-apikey`, `#fb-url`, `#fb-birth`, `#fb-result`,
  `#fb-hint`), and the new ones are `#fb-choices`, `#fb-detail` and
  `#fb-notes`. A copy of `index.html` from before 0.2.1 keeps its own form,
  which still begins a birth.
- **New files** you can read, and copy only to own: `components/<name>/`
  (each with a README), `shell/router.js`, `shell/page-note.js`,
  `shell/thread-place.js`, `views/chat/` (the thread and its parts),
  `views/home/`, `transport/channel.js`, `registry.js`, `strings/`.

A copy of `app.js` made before 0.2.1 still starts: the shell keeps every id
it finds, and its links to the four places. It draws none of what came
after it (the top bar's component, the tabs, the note about what moved).
A copy of `index.html` made before 0.2.1 keeps its rail, unstyled now: each
button still goes to the view it names, at its new address, and the open
one is still marked (`.active`, `aria-current`). Copy the shipped
`header#topbar` and `aside.c-rail` into it to have the bar and the
sidebar.

## The three invariants that survive every change

1. **Fail-closed to the compiled byte.** Every failure mode — absent,
   oversized (>1 MiB per file), wrong extension, escaping path, unreadable,
   directory — serves the frame as shipped. Deletion is always a restore.
2. **Containment by os.Root.** Every served path resolves inside this
   directory by the kernel; a symlink planted here cannot reach out of it.
   Served overlay code runs same-origin with dashboard authority and may
   re-form the view and speak to this server. It cannot make a background
   request to any other origin: `uiCSP` sets `default-src 'none'` and
   allows only `'self'` on every fetch-carrying directive (script, style,
   img, font, connect, frame) and on `form-action`. The browser enforces that
   per response. That is a limit on requests, not a promise that nothing
   a page shows can leave it: what you write here runs with everything
   your operator's page can see, and is trusted with it.
3. **Separate audit from live updates:** `overlays` (audit readback, retained
   events, `decidedAt` stamps) is render-only. `overlay_changed` (fresh
   monotonic token + changed paths) is the only live invalidation trigger.
   A page that applies audit events would loop forever and miss real edits.
   Audit is history; this is news.

## How to iterate with your operator, live

1. Ask what they want changed, or propose options grounded in the served
   DOM (`#bar-mark`, `#slot-panel`/`.panel-col`, `#thread`, `#msg-input`,
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

## Known limitations

- **Duplicate `overlay_changed` frames observed** (same token delivered
  twice back-to-back) on the dashboard socket. The client's monotonic
  token guard makes the second a strict no-op. Duplicate emission remains
  a known limitation.

## Escalation

If a change you need requires behavior in the frame's own scripts (not
just additive composition), that is a source change in the Go repo — take
it to your operator. The overlay surface is deliberately bounded: one
MiB per file, three extensions, one directory, kernel-contained.
