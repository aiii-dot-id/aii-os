package dashboard

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
)

type ThinkingRecord struct {
	Models       string
	CreatedAt    string
	MayBeSummary bool
	Content      string
}

const thinkingPartRunes = 64000

type thinkingAnswer struct {
	Version      int    `json:"version"`
	Turn         string `json:"turn"`
	Recorded     bool   `json:"recorded"`
	Models       string `json:"models,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	MayBeSummary bool   `json:"may_be_summary,omitempty"`
	Part         int    `json:"part,omitempty"`
	Parts        int    `json:"parts,omitempty"`
	Text         string `json:"text,omitempty"`
}

func (s *Server) serveInteractionThinking(w http.ResponseWriter, r *http.Request) {
	s.jobsMu.Lock()
	if s.jobsStopping {
		s.jobsMu.Unlock()
		http.Error(w, "host stopping", http.StatusServiceUnavailable)
		return
	}
	s.jobsWG.Add(1)
	s.jobsMu.Unlock()
	defer s.jobsWG.Done()
	if !s.tokenAuthorized(r) {
		http.Error(w, "dashboard access token required", http.StatusUnauthorized)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameSiteAs(origin, r.Host) {
		http.Error(w, "foreign origin", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h := s.currentHandler()
	if h == nil || h.Thinking == nil {
		http.Error(w, "thinking unavailable", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	rawTurn := q.Get("turn")
	turn, err := strconv.ParseUint(rawTurn, 10, 64)
	if err != nil || turn == 0 || turn > math.MaxInt64 || strconv.FormatUint(turn, 10) != rawTurn {
		http.Error(w, "invalid turn", http.StatusBadRequest)
		return
	}
	part := 1
	if raw := q.Get("part"); raw != "" {
		if part, err = strconv.Atoi(raw); err != nil || part < 1 {
			http.Error(w, "invalid part", http.StatusBadRequest)
			return
		}
	}
	rec, ok, err := h.Thinking(r.Context(), turn)
	if h != s.currentHandler() {
		http.Error(w, "history owner replaced", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "thinking unavailable", http.StatusInternalServerError)
		return
	}
	answer := thinkingAnswer{Version: 1, Turn: rawTurn}
	if ok {
		runes := []rune(rec.Content)
		parts := (len(runes) + thinkingPartRunes - 1) / thinkingPartRunes
		if parts < 1 {
			parts = 1
		}
		if part > parts {
			http.Error(w, "invalid part", http.StatusBadRequest)
			return
		}
		lo, hi := (part-1)*thinkingPartRunes, part*thinkingPartRunes
		if hi > len(runes) {
			hi = len(runes)
		}
		answer.Recorded, answer.Models, answer.CreatedAt, answer.MayBeSummary = true, rec.Models, rec.CreatedAt, rec.MayBeSummary
		answer.Part, answer.Parts, answer.Text = part, parts, string(runes[lo:hi])
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}
