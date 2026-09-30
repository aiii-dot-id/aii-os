package dashboard

import (
	"context"
	"time"

	"github.com/coder/websocket"
)

type MessagesRequest struct {
	Limit int `json:"limit,omitempty"`
}

type MessagesState struct {
	Arrivals       []ArrivalRow `json:"arrivals"`
	ArrivalsTotal  int          `json:"arrivals_total"`
	Open           []SendRow    `json:"open"`
	OpenTotal      int          `json:"open_total"`
	Delivered      []SendRow    `json:"delivered"`
	DeliveredTotal int          `json:"delivered_total"`
	Limit          int          `json:"limit"`
	MaxLimit       int          `json:"max_limit"`

	Notices      []NoticeRow `json:"notices"`
	NoticesTotal int         `json:"notices_total"`

	Channels []string `json:"channels"`
	ReadMs   int64    `json:"read_ms"`
}

type NoticeRow struct {
	ID          string `json:"id"`
	Body        string `json:"body"`
	BodyChars   int    `json:"body_chars"`
	CreatedMs   int64  `json:"created_ms"`
	Route       string `json:"route,omitempty"`
	State       string `json:"state"`
	Note        string `json:"note,omitempty"`
	Answer      string `json:"answer,omitempty"`
	Attempts    int    `json:"attempts"`
	Via         string `json:"via,omitempty"`
	DeliveredAt string `json:"delivered_at,omitempty"`
	SentMs      int64  `json:"sent_ms,omitempty"`
}

type NoticeRoute struct {
	On    bool         `json:"on"`
	Lines []NoticeLine `json:"lines"`
	Held  string       `json:"held,omitempty"`
}

type NoticeLine struct {
	Name    string `json:"name"`
	Channel string `json:"channel"`
	Carried bool   `json:"carried"`
}

type ArrivalRow struct {
	ID        string `json:"id"`
	Channel   string `json:"channel"`
	Address   string `json:"address"`
	From      string `json:"from,omitempty"`
	Relayed   string `json:"relayed,omitempty"`
	Body      string `json:"body"`
	BodyChars int    `json:"body_chars"`
	At        int64  `json:"received_ms"`
	Woke      bool   `json:"woke"`
	Read      bool   `json:"read"`
}

type SendRow struct {
	ID        string `json:"id"`
	To        string `json:"to"`
	Body      string `json:"body"`
	BodyChars int    `json:"body_chars"`
	CreatedMs int64  `json:"created_ms"`
	State     string `json:"state"`

	Note        string    `json:"note,omitempty"`
	Answer      string    `json:"answer,omitempty"`
	Attempts    int       `json:"attempts"`
	Via         string    `json:"via,omitempty"`
	DeliveredAt string    `json:"delivered_at,omitempty"`
	SentMs      int64     `json:"sent_ms,omitempty"`
	Activity    *Activity `json:"activity,omitempty"`
}

type Activity struct {
	SinceMs     int64        `json:"since_ms"`
	Count       int          `json:"count"`
	Latest      []ArrivalRow `json:"latest"`
	Unavailable string       `json:"unavailable,omitempty"`
}

func (s *Server) handleMessages(parent context.Context, conn *websocket.Conn, h *WSHandler, msg ClientMessage) {
	if h == nil || h.Messages == nil {
		s.sendErrorFor(parent, conn, msg.RequestID, "messages are unavailable until an identity is live")
		return
	}
	req := MessagesRequest{}
	if msg.Messages != nil {
		req = *msg.Messages
	}
	if !s.background(h, func() {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		state, err := h.Messages(ctx, req)
		if err != nil {
			s.sendErrorFor(ctx, conn, msg.RequestID, "messages could not be read: "+err.Error())
			return
		}
		s.sendMsg(ctx, conn, ServerMessage{Type: "messages", RequestID: msg.RequestID, Messages: &state})
	}) {
		s.sendErrorFor(parent, conn, msg.RequestID, "application is stopping; messages were not read")
	}
}
