package dashboard

import (
	"context"
	"fmt"
)

func (s *Server) wsProbe(ctx context.Context, conn carrier, msg ClientMessage) {

	active := false
	if h := s.currentHandler(); h.TurnActive != nil {
		active = h.TurnActive()
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "probe", RequestID: msg.RequestID, TurnActive: active})
}

func (s *Server) wsAsk(ctx context.Context, conn carrier, msg ClientMessage) {

	h := s.currentHandler()
	if msg.Ask == nil || h.AnswerAsk == nil {
		s.sendError(ctx, conn, "ask: nothing to answer")
		return
	}
	text, settle, err := h.AnswerAsk(*msg.Ask)
	if err != nil {
		s.sendError(ctx, conn, err.Error())
		return
	}
	s.BroadcastAsks()
	if text != "" {
		s.runOperatorMessage(ctx, conn, h, text, func(r ChatRecording) {
			if settle != nil {
				settle(r.Error == nil)
			}
		})
	}
}

func (s *Server) wsCancel(ctx context.Context, conn carrier) {

	h := s.currentHandler()
	if h.CancelTurn != nil && h.CancelTurn() {
		s.sendMsg(ctx, conn, ServerMessage{Type: "cancelled"})
	} else {
		s.sendError(ctx, conn, "no turn is running to cancel")
	}
}

func (s *Server) wsGenesis(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if msg.Genesis == nil {
		s.sendError(ctx, conn, "genesis request is empty")
		return
	}
	if h.HandleGenesis == nil {
		s.sendError(ctx, conn, "genesis not configured")
		return
	}
	response, err := h.HandleGenesis(ctx, msg.Genesis)
	if err != nil {
		s.sendError(ctx, conn, fmt.Sprintf("genesis failed: %v", err))
		return
	}

	s.sendStatus(ctx, conn, s.currentHandler())
	s.sendMsg(ctx, conn, s.spokenAloud(ServerMessage{Type: "response", Message: response, Role: "identity", Done: true}))
}
