package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// .
// .
// .
const sayBodyBound = 32 << 10

// .
// .
// .
// .
// .
// .
// .
type SpeakText struct {
	Text     string `json:"text"`
	Sample   bool   `json:"sample,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Voice    string `json:"voice,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
}

// .
// .
// .
// .
// .
// .
// .
// .
func (s *Server) handleSpeechSay(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !s.tokenAuthorized(r) {
		sayRefused(w, http.StatusUnauthorized, "this dashboard needs its access token")
		return
	}
	// .
	// .
	// .
	// .
	// .
	if o := r.Header.Get("Origin"); o != "" && !sameSiteAs(o, r.Host) {
		sayRefused(w, http.StatusForbidden, "this request came from another site")
		return
	}
	h := s.currentHandler()
	if h == nil || h.SpeakMint == nil || h.SpeakPlay == nil {
		sayRefused(w, http.StatusServiceUnavailable, "this identity has no speaking service")
		return
	}
	var say SpeakText
	if err := json.NewDecoder(io.LimitReader(r.Body, sayBodyBound)).Decode(&say); err != nil {
		sayRefused(w, http.StatusBadRequest, "this is not a reply to speak")
		return
	}
	// .
	// .
	// .
	// .
	if say.Provider == "" && h.ReplyVoice != nil && h.ReplyVoice() == "" {
		sayRefused(w, http.StatusServiceUnavailable, "this identity has no speaking service")
		return
	}
	id, err := h.SpeakMint(say)
	if err != nil {
		sayRefused(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

// .
// .
// .
// .
// .
// .
// .
func (s *Server) handleSpeechPlay(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !s.tokenAuthorized(r) {
		sayRefused(w, http.StatusUnauthorized, "this dashboard needs its access token")
		return
	}
	h := s.currentHandler()
	if h == nil || h.SpeakPlay == nil {
		sayRefused(w, http.StatusServiceUnavailable, "this identity has no speaking service")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		sayRefused(w, http.StatusBadRequest, "no reply was named")
		return
	}
	// .
	// .
	// .
	out := &sayStream{w: w}
	if err := h.SpeakPlay(r.Context(), id, out); err != nil {
		if out.wrote {
			// .
			return
		}
		if r.Context().Err() != nil {
			return
		}
		sayRefused(w, http.StatusBadGateway, err.Error())
		return
	}
}

// .
// .
type sayStream struct {
	w     http.ResponseWriter
	wrote bool
}

func (s *sayStream) Write(p []byte) (int, error) {
	if !s.wrote {
		s.w.Header().Set("Content-Type", "audio/wav")
		s.w.Header().Set("X-Content-Type-Options", "nosniff")
		s.w.Header().Set("Accept-Ranges", "none")
		s.w.WriteHeader(http.StatusOK)
		s.wrote = true
	}
	return s.w.Write(p)
}

func (s *sayStream) Flush() {
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

// .
// .
// .
// .
func (s *Server) spokenAloud(msg ServerMessage) ServerMessage {
	if msg.Type != "response" || !msg.Done || msg.Role != "identity" || msg.VoiceReply != nil || strings.TrimSpace(msg.Message) == "" {
		return msg
	}
	h := s.currentHandler()
	if h == nil || h.SpeakAhead == nil || s.screens() == 0 {
		return msg
	}
	if id := h.SpeakAhead(msg.Message); id != "" {
		msg.VoiceReply = &VoiceReplyRef{SynthesisID: id, Route: "cloud"}
	}
	return msg
}

// .
// .
func (s *Server) screens() int {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	return len(s.wsConns)
}

// .
func sameSiteAs(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Host == host
}

// .
// .
func sayRefused(w http.ResponseWriter, status int, why string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": why})
}
