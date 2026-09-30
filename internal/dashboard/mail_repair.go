package dashboard

import (
	"context"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/coder/websocket"
)

type MailRoute struct {
	Name    string `json:"name"`
	Channel string `json:"channel"`
}
type MailRepairRequest struct {
	ID        string `json:"id"`
	SHA256    string `json:"sha256"`
	Recipient string `json:"recipient"`
	Channel   string `json:"channel"`
}
type MailRepairState struct {
	Messages []store.HeldMessage `json:"messages"`
	Routes   []MailRoute         `json:"routes"`
	Next     string              `json:"next,omitempty"`
	Queued   string              `json:"queued,omitempty"`
}

func (s *Server) handleMailRepair(parent context.Context, conn *websocket.Conn, h *WSHandler, msg ClientMessage) {
	if h == nil || h.HeldMail == nil || (msg.Type == "repair_mail" && (h.RepairMail == nil || msg.MailRepair == nil)) {
		s.sendErrorFor(parent, conn, msg.RequestID, "mail repair is unavailable")
		return
	}
	if !s.background(h, func() {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		queued := ""
		if msg.Type == "inspect_mail" {
			if h.InspectMail == nil || msg.MailRepair == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "mail inspection unavailable")
				return
			}
			state, err := h.InspectMail(ctx, msg.MailRepair.ID)
			if err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
				return
			}
			s.sendMsg(ctx, conn, ServerMessage{Type: "held_mail", RequestID: msg.RequestID, MailRepair: &state})
			return
		}
		if msg.Type == "repair_mail" {
			if err := h.RepairMail(ctx, *msg.MailRepair); err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
				return
			}
			queued = msg.MailRepair.ID
		}
		state, err := h.HeldMail(ctx, msg.Name)
		if err != nil {
			if queued != "" {
				s.sendErrorFor(ctx, conn, msg.RequestID, "correction committed and message queued; refresh failed: "+err.Error())
			} else {
				s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
			}
			return
		}
		state.Queued = queued
		s.sendMsg(ctx, conn, ServerMessage{Type: "held_mail", RequestID: msg.RequestID, MailRepair: &state})
	}) {
		s.sendErrorFor(parent, conn, msg.RequestID, "application is stopping; no repair started")
	}
}
