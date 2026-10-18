package dashboard

import (
	"context"
	"net/url"
	"strings"
)

func (s *Server) wsAccessCode(ctx context.Context, conn carrier, cl *wsClient, msg ClientMessage) {
	if msg.Type == "access_code_cancel" {
		s.cancelAccessCode(msg.AccessCode)
		s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "access_code"})
		return
	}
	if !cl.secretsOK {
		s.sendErrorFor(ctx, conn, msg.RequestID, ErrAccessCodeNotPrivate.Error())
		return
	}
	if h := s.currentHandler(); h != nil && h.AccessCodeSeconds != nil {
		s.SetAccessCodeSeconds(h.AccessCodeSeconds())
	}
	st, err := s.issueAccessCode(cl.policy, msg.AccessCode)
	if err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
		return
	}
	origin := ""
	if h := s.currentHandler(); h != nil && h.AccessCodeOrigin != nil {
		origin = h.AccessCodeOrigin()
	}
	if origin == "" {
		origin = s.Origin()
	}
	st.Link = accessCodeLink(origin, st.Code)
	st.QR = qrSVG(st.Link)
	s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "access_code", AccessCode: &st})
}

func accessCodeLink(origin, shown string) string {
	return strings.TrimSuffix(origin, "/") + "/auth/link#" + url.PathEscape(normalizeAccessCode(shown))
}
