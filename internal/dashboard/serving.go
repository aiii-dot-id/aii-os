package dashboard

import (
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

func (s *Server) SetServingKey(key []byte) {
	k := append([]byte(nil), key...)
	s.servingKey.Store(&k)
}

func (s *Server) handleServing(w http.ResponseWriter, r *http.Request) {
	if !remoteIsLoopback(r.RemoteAddr) {
		http.Error(w, "this is answered only to a program on this machine", http.StatusForbidden)
		return
	}
	key := s.servingKey.Load()
	if key == nil {
		http.NotFound(w, r)
		return
	}
	challenge, err := hex.DecodeString(r.URL.Query().Get("c"))
	if err != nil || len(challenge) < 16 || len(challenge) > 64 {
		http.Error(w, "the challenge is 16 to 64 bytes, in hex", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, hex.EncodeToString(install.ServingProof(*key, challenge)))
}
