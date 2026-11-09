package dashboard

import "context"

func (s *Server) wsPassNow(ctx context.Context, conn carrier, h *WSHandler, msg ClientMessage) {
	if h.PassNow == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
		return
	}
	res, err := h.PassNow(ctx)
	if err != nil {

		s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "pass_now", RequestID: msg.RequestID, PassNow: &res})
}
