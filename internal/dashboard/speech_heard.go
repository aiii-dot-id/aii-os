package dashboard

import (
	"net/http"
	"strconv"
)

func (s *Server) handleSpeechHeard(w http.ResponseWriter, r *http.Request) {
	if !s.tokenAuthorized(r) {
		sayRefused(w, http.StatusUnauthorized, "this dashboard needs its access token")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameSiteAs(origin, r.Host) {
		sayRefused(w, http.StatusForbidden, "this request came from another site")
		return
	}
	h := s.currentHandler()
	if h == nil || h.HeardSpeech == nil {
		sayRefused(w, http.StatusServiceUnavailable, "this host holds no heard speech")
		return
	}
	var after uint64
	if raw := r.URL.Query().Get("after"); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			sayRefused(w, http.StatusBadRequest, "after is a row's seq, a whole number")
			return
		}
		after = n
	}
	page, err := h.HeardSpeech(after)
	if err != nil {
		sayRefused(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(page))
}
