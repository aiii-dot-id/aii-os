// custom.js — the additive behaviour layer. Empty as shipped.
//
// An ES module loaded LAST, after app.js has booted the frame, so it
// observes a live DOM and a running app. Drop your own copy at
// <data dir>/ui/custom.js and it is served instead of this empty one
// (R71's overlay; no rebuild, no restart).
//
// Prefer this over overlaying app.js or a views/ module. Replacing a
// shipped module means owning a fork that talks to a WS protocol which
// keeps moving without it — accepted by the overlay, silently wrong.
// Adding here forks nothing.
//
// Authority: same-origin, identical to the rest of the frame, bounded
// by uiCSP — no fetch directive names an external origin, so this
// may re-form the view and speak to this server, never beacon out.
// See docs/THREAT_MODEL-ui-disk-overlay.md.
