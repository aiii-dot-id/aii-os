package dashboard

import (
	"context"
)

func (s *Server) wsPublicName(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	var act func() (PublicNameState, error)
	switch {
	case msg.Type == "public_name_claim" && h.PublicNameClaim != nil:
		act = h.PublicNameClaim
	case msg.Type == "public_name_retry" && h.PublicNameRetry != nil:
		act = h.PublicNameRetry
	case msg.Type == "public_name_move" && h.PublicNameMove != nil:
		act = h.PublicNameMove
	case msg.Type == "public_name_look" && h.PublicNameLook != nil:
		act = h.PublicNameLook
	case msg.Type == "public_name_state" && h.PublicNameState != nil:
		st := h.PublicNameState()
		s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "public_name", PublicName: &st})
		return
	default:
		s.sendErrorFor(ctx, conn, msg.RequestID, "public names are not available")
		return
	}

	reqID := msg.RequestID
	s.offLoop(ctx, conn, reqID, h, func() {
		st, err := act()
		if err != nil {
			s.sendErrorFor(ctx, conn, reqID, err.Error())
			return
		}
		s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "public_name", PublicName: &st})
	})
}
