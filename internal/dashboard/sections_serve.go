// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

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

// .
// .
// .
func (s *Server) SetSections(reg *sections.Registry) {
	s.secMu.Lock()
	s.secReg = reg
	s.secMu.Unlock()
}

// .
// .
// .
func (s *Server) SetLayoutSource(fn func() []byte) {
	s.secMu.Lock()
	s.layoutSource = fn
	s.secMu.Unlock()
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (s *Server) SetUIOverlay(dir string) {
	s.secMu.Lock()
	s.overlayDir = dir
	// .
	// .
	s.overlayReported = nil
	s.secMu.Unlock()
}

// .
// .
// .
// .
// .
func (s *Server) SetBuildStamp(stamp string) {
	s.secMu.Lock()
	s.buildStamp = stamp
	s.secMu.Unlock()
}

// .
// .
// .
// .
const maxOverlayBytes = 1 << 20

// .
// .
// .
// .
// .
// .
func readAssetBounded(f io.Reader, cap int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(f, cap+1))
	if err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}
	if int64(len(data)) > cap {
		return nil, fmt.Errorf("%d+ bytes exceeds the %d-byte ceiling", cap+1, cap)
	}
	return data, nil
}

// .
// .
const maxSectionAssetBytes = 16 << 20

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (s *Server) overlayAsset(p string) ([]byte, bool) {
	s.secMu.RLock()
	dir := s.overlayDir
	stamp := s.buildStamp
	s.secMu.RUnlock()
	if dir == "" {
		return nil, false
	}
	// .
	// .
	// .
	if _, ok := sectionServableTypes[path.Ext(p)]; !ok {
		s.reportOverlay(p, "rejected: not a frame extension")
		return nil, false
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
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
	data, err := readAssetBounded(f, maxOverlayBytes)
	if err != nil {
		s.reportOverlay(p, "inert: "+err.Error())
		return nil, false
	}
	s.reportOverlay(p, acceptedOutcome(p, data, stamp))
	return data, true
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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
				// .
				// .
				// .
				// .
				// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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

// .
// .
// .
var fontSizePx = regexp.MustCompile(`font-size\s*:\s*([0-9]*\.?[0-9]+)px`)

// .
// .
// .
// .
// .
// .
// .
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
	// .
	// .
	// .
	// .
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
	// .
	// .
	// .
	// .
	// .
	s.broadcast(s.overlayMessage())
}

// .
// .
// .
// .
const maxOverlayEvents = 32

// .
// .
// .
// .
// .
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

// .
// .
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

// .
// .
// .
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

// .
// .
// .
func (s *Server) overlayMessage() ServerMessage {
	s.secMu.RLock()
	events := append([]OverlayEvent(nil), s.overlayEvents...)
	s.secMu.RUnlock()
	return ServerMessage{Type: "overlays", Overlays: events}
}

// .
// .
// .
func (s *Server) BroadcastSections() { s.broadcast(s.sectionsMessage()) }

// .
// .
func (s *Server) BroadcastLayout() { s.broadcast(s.layoutMessage()) }

// .
// .
// .
// .
// .
// .
func (s *Server) BroadcastOverlay() { s.broadcast(s.overlayMessage()) }

// .
// .
// .
// .
// .
// .
func (s *Server) BroadcastOverlayChanged(token uint64, paths []string) {
	s.broadcast(ServerMessage{Type: "overlay_changed", Token: token, Paths: paths})
}

// .
// .
// .
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

// .

// .
// .
// .
var sectionServableTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
const uiCSP = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"style-src-elem 'self'; " +
	"style-src-attr 'unsafe-inline'; " +
	"img-src 'self'; " +
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	"media-src 'self' blob:; " +
	"connect-src 'self'; " +
	"frame-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"object-src 'none'"

func sectionCSP(scheme, host, id string) string {
	// .
	// .
	// .
	// .
	// .
	// .
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

// .
// .
// .
// .
func (s *Server) handleSectionFile(w http.ResponseWriter, r *http.Request) {
	// .
	// .
	// .
	if !s.tokenAuthorized(r) {
		http.NotFound(w, r)
		return
	}
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
			// .
			// .
			// .
			log.Printf("dashboard: dev section %q refused under SAFE (%s)", id, reason)
			http.NotFound(w, r)
			return
		}
	}
	rel := r.PathValue("path")
	// .
	// .
	// .
	// .
	if rel == "" || strings.Contains(rel, "\\") || path.Clean("/"+rel) != "/"+rel {
		http.NotFound(w, r)
		return
	}
	ctype, ok := sectionServableTypes[path.Ext(rel)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	// .
	// .
	// .
	// .
	// .
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
	// .
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	// .
	// .
	// .
	if st.Size() > maxSectionAssetBytes {
		http.Error(w, "section asset too large", http.StatusRequestEntityTooLarge)
		return
	}
	data, err := readAssetBounded(f, maxSectionAssetBytes)
	if err != nil {
		http.Error(w, "section asset too large", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Security-Policy", sectionCSP(requestScheme(r), r.Host, id))
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if sec.Dev {
		// .
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Write(data)
}
