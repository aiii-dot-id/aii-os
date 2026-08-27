// Section serving and frame furniture (R66 UP2, UI_FRAME.md §§3-5):
// GET /sections/<id>/<path> serves ONLY registered sections' files,
// every response behind the sandbox walls — strict CSP (no connect,
// own-path script/style), framing pinned to this page — and the WS
// grows the two frame queries ("sections", "ui_layout") plus their
// broadcast twins for hot layout reload and SAFE entry.
//
// The wall inventory, stated once: a section document runs in a
// sandboxed iframe with an OPAQUE origin (frame sets allow-scripts,
// never allow-same-origin). Its fetch/XHR/WS are killed by
// connect-src 'none' here — and even without CSP, a socket it opened
// would carry Origin "null", which wsAuthorized already refuses (the
// H2 gate: present, parseable, same-host http Origin). The server
// never trusted the browser; sections narrow that channel further and
// make misuse loud (§1: no new authority surface).

package dashboard

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/coder/websocket"
)

// SetSections wires the app-assembled section registry in (the
// facility-set pattern: assembled once, read through narrow methods).
// nil keeps the frame-only dashboard — today's behavior.
func (s *Server) SetSections(reg *sections.Registry) {
	s.secMu.Lock()
	s.secReg = reg
	s.secMu.Unlock()
}

// SetLayoutSource wires the ui-layout.json reader (the app owns the
// file, its watcher, and its validation; the dashboard only carries
// bytes to the frame).
func (s *Server) SetLayoutSource(fn func() []byte) {
	s.secMu.Lock()
	s.layoutSource = fn
	s.secMu.Unlock()
}

// SetUIOverlay wires the operator's frame overlay directory (T1).
// Empty string — the default — means the frame is served entirely from
// the embed and this code never touches the disk.
//
// FULL RE-FORM, BY OPERATOR RULING (James, 2026-08-24): "The operator
// and the AI identity should be able to override and re-form the UI."
// All three servable frame extensions — .html, .js, .css — may be
// replaced from disk. This deliberately reverses the presentation-only
// restriction this tier shipped with: a served overlay script runs
// same-origin with the dashboard's full authority, and the data dir is
// both-hands writable, so this grants an identity that can write its
// own data dir the ability to re-form its operator's UI. The operator
// judged that authority the point, not the risk. The invariants that
// survive the ruling: containment (os.Root), the extension allowlist,
// the byte ceiling, fail-closed fall-back to the compiled byte, and
// uiCSP with no external origin in any fetch directive — overlay
// code may re-form the view and speak to this server, never beacon out.
// See docs/THREAT_MODEL-ui-disk-overlay.md.
func (s *Server) SetUIOverlay(dir string) {
	s.secMu.Lock()
	s.overlayDir = dir
	// A new directory is a new set of answers; the old readback no
	// longer describes what is on disk.
	s.overlayReported = nil
	s.secMu.Unlock()
}

// SetBuildStamp wires the evaluating build into fork verdicts (the
// facility-set pattern: the app owns BuildIdentity, the dashboard only
// carries the string it computed). A build change under a frozen fork
// changes the verdict text itself, so the readback re-decides instead
// of silently letting the fork shadow bytes it never saw.
func (s *Server) SetBuildStamp(stamp string) {
	s.secMu.Lock()
	s.buildStamp = stamp
	s.secMu.Unlock()
}

// maxOverlayBytes bounds one overlay file. The largest shipped frame
// file is 32 KiB (layout.css); a full re-form is a handful of files of
// that order. Beyond this is a mistake, and a mistake falls back to the
// compiled byte rather than being served.
const maxOverlayBytes = 1 << 20

// overlayAsset returns the operator's replacement for frame path p
// (e.g. "/theme.css", "/app.js", "/index.html") and whether it was
// used. EVERY failure — no overlay configured, non-servable extension,
// absent, escaping, a directory, oversized, unreadable — returns false,
// and false means the caller serves the compiled byte. Fail closed
// toward the frame as shipped (AGENTS.md §1.4).
//
// Containment is os.Root: every segment of p is resolved inside the
// overlay dir by the kernel, so a symlink planted in ui/ cannot reach out
// of it. That is enforcement, not a string comparison — the same lesson
// the section server already paid for.
func (s *Server) overlayAsset(p string) ([]byte, bool) {
	s.secMu.RLock()
	dir := s.overlayDir
	stamp := s.buildStamp
	s.secMu.RUnlock()
	if dir == "" {
		return nil, false
	}
	// Same allowlist the frame mux switches on: only these three
	// extensions are frame, so only these three can be overridden. The
	// invariant lives in one place per side and neither can drift alone.
	if _, ok := sectionServableTypes[path.Ext(p)]; !ok {
		s.reportOverlay(p, "rejected: not a frame extension")
		return nil, false
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		// An ABSENT overlay dir is the default state of every install, not
		// a fault: it is the same absence as a missing file (below), one
		// level up, and reporting it emitted one inert line per frame
		// asset — 27 on a stock system — which is the flood the readback
		// exists to prevent. Observed on the running system at 10:42.
		// A dir that exists but cannot be opened is a real fault and is
		// still reported: that one an operator must act on.
		if !os.IsNotExist(err) {
			s.reportOverlay(p, "inert: overlay dir unopenable: "+err.Error())
		}
		return nil, false
	}
	defer root.Close()
	f, err := root.Open(strings.TrimPrefix(p, "/"))
	if err != nil {
		if !os.IsNotExist(err) {
			s.reportOverlay(p, "inert: unreadable or escaping: "+err.Error())
		}
		return nil, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.reportOverlay(p, "inert: unstattable: "+err.Error())
		return nil, false
	}
	if !st.Mode().IsRegular() {
		s.reportOverlay(p, "inert: not a regular file")
		return nil, false
	}
	if st.Size() > maxOverlayBytes {
		s.reportOverlay(p, fmt.Sprintf("inert: %d bytes exceeds the %d-byte ceiling", st.Size(), maxOverlayBytes))
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxOverlayBytes))
	if err != nil {
		s.reportOverlay(p, "inert: read failed: "+err.Error())
		return nil, false
	}
	s.reportOverlay(p, acceptedOutcome(p, data, stamp))
	return data, true
}

// acceptedOutcome distinguishes the two ways an overlay can be accepted,
// because they have opposite futures and a bare "accepted" hides that.
//
// custom.css and custom.js ship as empty stubs on purpose: overlaying
// them ADDS a layer that the cascade and the module order compose with
// the shipped frame, so a later release's fixes to layout.css or app.js
// still arrive. Nothing can diverge, because nothing was replaced.
//
// Overlaying any other shipped file REPLACES it, and that copy is frozen
// at the build it was taken from. The next release's fixes to that file
// will never reach it, and no error will ever be raised, because nothing
// failed: it is present, readable, valid and contained. Fail-closed
// cannot help here. This is exactly the silent-divergence shape that
// hand-copied policy takes everywhere else in this system — the readback
// has to name it at the moment the fork starts, which is the only moment
// anyone is looking.
func acceptedOutcome(p string, data []byte, build string) string {
	n := len(data)
	stamp := ""
	if build != "" {
		stamp = " at build " + build
	}
	var outcome string
	switch {
	case p == "/custom.css" || p == "/custom.js":
		outcome = fmt.Sprintf("accepted: additive layer (%d bytes) — composes with the shipped frame and keeps receiving its upgrades", n)
	default:
		shipped, err := staticFS.ReadFile("static" + p)
		if err != nil {
			outcome = fmt.Sprintf("accepted: new file (%d bytes) — no shipped counterpart, nothing to diverge from", n)
		} else {
			if bytes.Equal(data, shipped) {
				// A byte-identical copy carries the shipped baseline,
				// which the phone-width tests already govern. Warning
				// about it here would describe the shipped frame, not
				// a change anyone made. The fork verdict still lands:
				// owning the file is the divergence, not today's bytes.
				outcome = fmt.Sprintf("accepted: FORK of shipped frame (byte-identical to %s%s) — no divergence today, but this copy is frozen and will NOT receive upgrades to %s; prefer /custom.css or /custom.js unless you mean to own it", p, stamp, p)
				return outcome
			}
			outcome = fmt.Sprintf("accepted: FORK of shipped frame (%d bytes replacing %d%s) — this copy is frozen and will NOT receive upgrades to %s; prefer /custom.css or /custom.js unless you mean to own it", n, len(shipped), stamp, p)
		}
	}
	if h := zoomHazard(p, data); h != "" {
		outcome += " " + h
	}
	return outcome
}

// zoomHazard names an accepted overlay that will make iOS zoom and stay
// zoomed. It does NOT refuse it: R71 grants both hands the override, and
// a surface that silently overrules its author is the failure this file
// exists to prevent. It says what will happen, at acceptance, which is
// the only moment anyone is looking.
//
// This one hazard is named because it is INVISIBLE from the change.
// layout.css:336 states the invariant in the mobile block — "inputs hit
// 16px so iOS never zoom-jumps" — and a form control under 16px makes
// mobile Safari zoom on focus and never zoom back: the presence strip
// leaves the screen and the nav clips for the rest of the session.
// Nothing connects "I made my chat text smaller" to that. custom.css
// loads last by design, so an equal-specificity rule there wins at every
// width; the shipped frame lost this once already to specificity alone
// (.composer textarea outranking a bare textarea, fixed 657c8d7).
//
// A HINT, not a verdict. This reads declarations, it does not resolve the
// cascade — a rule that never matches anything still trips it. That is
// the right way round: a false warning costs a glance, and a silent one
// costs the operator their whole session.
func zoomHazard(p string, data []byte) string {
	if !strings.HasSuffix(p, ".css") {
		return ""
	}
	for _, block := range strings.Split(string(data), "}") {
		open := strings.LastIndex(block, "{")
		if open < 0 {
			continue
		}
		sel := strings.ToLower(block[:open])
		if !strings.Contains(sel, "textarea") && !strings.Contains(sel, "input") && !strings.Contains(sel, "select") {
			continue
		}
		m := fontSizePx.FindStringSubmatch(strings.ToLower(block[open+1:]))
		if m == nil {
			continue
		}
		px, err := strconv.ParseFloat(m[1], 64)
		if err != nil || px >= 16 {
			continue
		}
		return fmt.Sprintf("HAZARD: this sets a form control to %spx. Under 16px, iOS zooms on focus and does not zoom back — the presence strip leaves the screen and the nav clips until reload. Use 16px or larger on phones.", m[1])
	}
	return ""
}

// fontSizePx matches a px font-size declaration. Deliberately narrow: rem
// and em depend on a root this cannot resolve, and guessing at them would
// produce the noisy warnings that stop being read.
var fontSizePx = regexp.MustCompile(`font-size\s*:\s*([0-9]*\.?[0-9]+)px`)

// reportOverlay is the readback an operator-authorable surface owes its
// author: accepted, rejected, or inert — once per path per outcome, so a
// per-request path does not become a log flood. A re-form surface that
// ignores you in silence is indistinguishable from one that obeyed you
// and happened to look the same, and that ambiguity is the whole defect
// this exists to remove. File-absent is deliberately NOT reported:
// overriding only part of the frame is the normal case, not a mistake.
func (s *Server) reportOverlay(p, outcome string) {
	key := p + "\x00" + outcome
	s.secMu.Lock()
	if s.overlayReported == nil {
		s.overlayReported = make(map[string]bool)
	}
	if s.overlayReported[key] {
		s.secMu.Unlock()
		return
	}
	s.overlayReported[key] = true
	// W2: keep the same event for the operator's screen, not just the
	// log. Bounded: a surface that produces unbounded distinct outcomes
	// (many paths × many outcome strings) cannot grow this without
	// limit — the tail keeps the newest.
	if len(s.overlayEvents) >= maxOverlayEvents {
		copy(s.overlayEvents, s.overlayEvents[1:])
		s.overlayEvents = s.overlayEvents[:len(s.overlayEvents)-1]
	}
	s.overlayEvents = append(s.overlayEvents, OverlayEvent{
		Path:      p,
		Outcome:   outcome,
		DecidedAt: time.Now().UTC().Format(time.RFC3339),
	})
	s.secMu.Unlock()
	log.Printf("dashboard: frame overlay %s: %s", p, outcome)
	// W2 push: a decision the operator never queried for still reaches
	// the screen the moment it is made. Same idiom as BroadcastLayout
	// (fan current state, not a delta — the last push wins). The dedup
	// above bounds this to once per path+outcome, so no surface can
	// turn it into a flood.
	s.broadcast(s.overlayMessage())
}

// maxOverlayEvents bounds the W2 event list. 32 is far beyond any sane
// overlay (the frame ships ~27 servable assets; a real overlay is a
// handful of files) and small enough that the worst case is a panel,
// not a scroll.
const maxOverlayEvents = 32

// SetThemeSource wires the theme.json reader (T0). Same division as
// the layout: the app owns the file, its watcher, and its VALIDATION
// — the dashboard only carries already-validated bytes to the frame.
// The frame document has no CSP, so what travels here has been
// re-encoded from the validated token map, never passed through raw.
func (s *Server) SetThemeSource(fn func() []byte) {
	s.secMu.Lock()
	s.themeSource = fn
	s.secMu.Unlock()
}

func (s *Server) themeBytes() []byte {
	s.secMu.RLock()
	fn := s.themeSource
	s.secMu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn()
}

func (s *Server) themeMessage() ServerMessage {
	return ServerMessage{Type: "theme", Theme: s.themeBytes()}
}

// BroadcastTheme fans the current tokens — the hot-reload push for
// the both-hands-edited theme.json.
func (s *Server) BroadcastTheme() { s.broadcast(s.themeMessage()) }

func (s *Server) sectionsRegistry() *sections.Registry {
	s.secMu.RLock()
	defer s.secMu.RUnlock()
	return s.secReg
}

func (s *Server) layoutBytes() []byte {
	s.secMu.RLock()
	fn := s.layoutSource
	s.secMu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn()
}

// sectionStates snapshots the registry for the frame. Under SAFE the
// list is EMPTY with the reason attached — all sections unmount, bare
// frame (§3), the same principle as origin-gated tool suspension.
func (s *Server) sectionStates() ([]SectionState, string) {
	reg := s.sectionsRegistry()
	if reg == nil {
		return nil, ""
	}
	if reason, safe := reg.Safe(); safe {
		return nil, reason
	}
	var out []SectionState
	for _, sec := range reg.List() {
		out = append(out, SectionState{
			ID: sec.Decl.ID, Title: sec.Decl.Title, Slot: sec.Decl.Slot,
			Commands: sec.Decl.Commands, Topics: sec.Decl.Topics,
			Entry: sec.Decl.Entry, Dev: sec.Dev,
		})
	}
	return out, ""
}

func (s *Server) sectionsMessage() ServerMessage {
	states, reason := s.sectionStates()
	return ServerMessage{Type: "sections", Sections: states, Message: reason}
}

func (s *Server) layoutMessage() ServerMessage {
	return ServerMessage{Type: "layout", Layout: s.layoutBytes()}
}

// overlayMessage is the W2 readback-to-human: the same outcomes the
// log carries, as a queryable message. Same division as sections/
// layout/theme — server state, no handler, mode-independent.
func (s *Server) overlayMessage() ServerMessage {
	s.secMu.RLock()
	events := append([]OverlayEvent(nil), s.overlayEvents...)
	s.secMu.RUnlock()
	return ServerMessage{Type: "overlays", Overlays: events}
}

// BroadcastSections fans the current section list to every live
// connection — called on SAFE entry so mounted sections leave the
// operator's screen immediately, not at the next poll.
func (s *Server) BroadcastSections() { s.broadcast(s.sectionsMessage()) }

// BroadcastLayout fans the current layout — the hot-reload push for
// the both-hands-edited ui-layout.json (§5).
func (s *Server) BroadcastLayout() { s.broadcast(s.layoutMessage()) }

// BroadcastOverlay fans the current overlay readback — the hot-reload
// push for the both-hands-edited <data dir>/ui/ directory (W3). An
// edit on disk must reach the operator's screen without an F5, and
// the readback is what tells the screen both that bytes moved AND
// what the resolver decided about them (accepted/rejected/inert,
// fork-named). Same idiom as BroadcastTheme and BroadcastLayout.
func (s *Server) BroadcastOverlay() { s.broadcast(s.overlayMessage()) }

// BroadcastOverlayChanged pushes the LIVE invalidation (P1 fix,
// 2026-08-25): overlay bytes moved on disk, and an already-open page
// must re-apply. Distinct from the audit readback by construction —
// the loop defect was one message type serving both masters. The
// caller (the app's watcher) owns the monotonic token and the path
// diff; the dashboard only carries them.
func (s *Server) BroadcastOverlayChanged(token uint64, paths []string) {
	s.broadcast(ServerMessage{Type: "overlay_changed", Token: token, Paths: paths})
}

// broadcast fans one message to every live connection with the same
// bounded-write discipline as PushTransient (a stalled tab must not
// wedge a broadcast).
func (s *Server) broadcast(msg ServerMessage) {
	s.wsMu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.wsConns))
	for c := range s.wsConns {
		conns = append(conns, c)
	}
	s.wsMu.Unlock()
	for _, c := range conns {
		ctx, cancel := context.WithTimeout(context.Background(), writeWait)
		s.sendMsg(ctx, c, msg)
		cancel()
	}
}

// --- serving ---

// sectionServableTypes is the section extension allowlist — the same
// deterministic extension switch the frame tree uses (UP1): browsers
// hard-refuse mistyped modules, and OS MIME tables are not truth.
var sectionServableTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
}

// sectionCSP builds the per-response wall for one section id. host is
// the request's Host header, already validated by hostGate (only this
// server's own loopback names survive it). Scope: scripts and styles
// only from the section's own path (plus the frame-owned
// /section-api.js client); no connections at all; framing only by
// this same page.
// uiCSP is the policy for the frame itself — the shell served at
// "/" — and it lives here, beside sectionCSP, so the two policies are
// read together rather than discovered separately.
//
// WHY IT CAN BE THIS TIGHT. Measured in the shipped tree, not assumed:
// one <script> tag and it is external, zero inline event handlers, zero
// eval or Function(), zero external origins, zero <img>, zero @font-face,
// zero url() in CSS, and nothing anywhere calls createElement('style'),
// insertRule, or cssText. Every fetch directive therefore collapses to
// 'self' or 'none', and script-src needs no 'unsafe-inline'.
//
// THE ONE CONCESSION is inline style ATTRIBUTES: 54 of them live inside
// innerHTML strings across views/. Attributes cannot carry a nonce, so
// style-src-attr must allow them. It is scoped to attributes ALONE —
// style-src-elem stays 'self', so an injected <style> block still cannot
// apply. That split is only purchasable because theme.js applies tokens
// with setProperty instead of generating a <style> block: the
// no-parse-step choice made there is what buys the strict element policy
// here.
//
// style-src is repeated as the CSP2 fallback. A browser that does not
// implement the -elem/-attr split ignores those two directives and uses
// style-src, so the frame degrades to functional-but-looser rather than
// to broken.
//
// WHY NOW, BEFORE ANY DISK OVERLAY. A missing policy is defensible while
// the frame serves only bytes we compiled. The moment operator files are
// served from disk it is load-bearing, and retrofitting a policy onto a
// UI that already ships user CSS means negotiating with breakage instead
// of declaring an invariant.
const uiCSP = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"style-src-elem 'self'; " +
	"style-src-attr 'unsafe-inline'; " +
	"img-src 'self'; " +
	"connect-src 'self'; " +
	"frame-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"object-src 'none'"

func sectionCSP(scheme, host, id string) string {
	// https, and these are CSP SOURCE EXPRESSIONS rather than links —
	// which is why the scheme matters more here than it looks. A source
	// written http:// does not match a resource served over https, so
	// the browser would block every section's own script and stylesheet
	// while the page itself loaded fine: a section plugin that shows a
	// blank panel and no error the operator can act on.
	own := scheme + "://" + host + "/sections/" + id + "/"
	api := scheme + "://" + host + "/section-api.js"
	return "default-src 'none'; " +
		"script-src 'self' " + own + " " + api + "; " +
		"style-src 'self' " + own + "; " +
		"img-src 'self' " + own + " data:; " +
		"connect-src 'none'; " +
		"frame-ancestors 'self'; " +
		"base-uri 'none'; " +
		"form-action 'none'"
}

// handleSectionFile serves one file of one REGISTERED section.
// Everything else — unknown ids, unregistered sections, non-allowlisted
// extensions, traversal shapes, dev sections under SAFE — is 404: the
// route does not discuss what it will not serve.
func (s *Server) handleSectionFile(w http.ResponseWriter, r *http.Request) {
	reg := s.sectionsRegistry()
	if reg == nil {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	sec, ok := reg.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if sec.Dev {
		if reason, safe := reg.Safe(); safe {
			// SAFE refuses dev sections ENTIRELY (§3): unverified bytes
			// have no business on a screen whose runtime cannot vouch
			// for itself.
			log.Printf("dashboard: dev section %q refused under SAFE (%s)", id, reason)
			http.NotFound(w, r)
			return
		}
	}
	rel := r.PathValue("path")
	// Fence: forward slashes, no empty/dot/dot-dot segments. The mux
	// cleans request paths already; this is the extractor's own rule
	// re-applied at the serving edge (dev dirs never went through the
	// extractor, so the edge must carry its own belt).
	if rel == "" || strings.Contains(rel, "\\") || path.Clean("/"+rel) != "/"+rel {
		http.NotFound(w, r)
		return
	}
	ctype, ok := sectionServableTypes[path.Ext(rel)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Containment is the OS's job, not a string comparison's. os.Root
	// resolves every segment inside the section directory and refuses
	// any escape, including through a symlink — which filepath.Rel
	// cannot see, because it compares names and never touches disk.
	// One owner for the invariant: the kernel, not this function.
	root, err := os.OpenRoot(sec.Dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	f, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	// Directories and devices are not section assets.
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Security-Policy", sectionCSP(s.Scheme(), r.Host, id))
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if sec.Dev {
		// The co-edit loop: every reload re-reads the operator's disk.
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Write(data)
}
