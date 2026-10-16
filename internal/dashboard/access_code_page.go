package dashboard

import (
	_ "embed"
	"net/http"
)

//go:embed linkpage/link.html
var accessCodeLinkHTML []byte

//go:embed linkpage/link.js
var accessCodeLinkJS []byte

func (s *Server) handleAccessCodeLink(w http.ResponseWriter, r *http.Request) {
	serveAccessCodePage(w, "text/html; charset=utf-8", accessCodeLinkHTML)
}

func (s *Server) handleAccessCodeLinkJS(w http.ResponseWriter, r *http.Request) {
	serveAccessCodePage(w, "text/javascript; charset=utf-8", accessCodeLinkJS)
}

func serveAccessCodePage(w http.ResponseWriter, ctype string, body []byte) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Security-Policy", uiCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
}
