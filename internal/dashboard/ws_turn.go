package dashboard

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
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
		s.sendErrorFor(ctx, conn, msg.RequestID, "ask: nothing to answer")
		return
	}
	text, settle, err := h.AnswerAsk(*msg.Ask)
	if err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
		s.queryAsks(ctx, conn, h)
		return
	}
	s.BroadcastAsks()
	said := interaction.SaidAnswer
	if msg.Ask.Answer == "said" {
		said = interaction.SaidTyped
	}
	if text != "" {
		requestID, askID := msg.RequestID, msg.Ask.ID
		s.runOperatorMessage(withRefusalOnReceipt(interaction.WithSaid(ctx, said)), conn, h, text, func(r ChatRecording) {
			if settle != nil {
				settle(r.Error == nil)
			}
			if r.Error == nil {
				return
			}

			if s.client(conn) != nil && askOpen(h, askID) {
				s.sendErrorFor(ctx, conn, requestID, answerNotRecorded(r.Error.Message))
			}
			s.BroadcastAsks()
		})
	}
}

func askOpen(h *WSHandler, id string) bool {
	if h.GetAsks == nil {
		return false
	}
	return slices.ContainsFunc(h.GetAsks(), func(a AskView) bool { return a.ID == id })
}

func answerNotRecorded(why string) string {
	return "your answer was not recorded (" + why + "), so the question is still open."
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

		var refused *RestoreNewRefusal
		if errors.As(err, &refused) {
			s.sendError(ctx, conn, refused.Text)
			return
		}
		s.sendError(ctx, conn, fmt.Sprintf("genesis failed: %v", err))
		return
	}

	s.sendStatus(ctx, conn, s.currentHandler())
	s.sendMsg(ctx, conn, s.spokenAloud(ServerMessage{Type: "response", Message: response, Role: "identity", Done: true}))
}
