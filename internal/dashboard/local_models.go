package dashboard

import (
	"context"
	"fmt"
)

type LocalModelState struct {
	Marked int `json:"marked"`

	Provider string `json:"provider,omitempty"`

	Model string `json:"model,omitempty"`

	Problem string `json:"problem,omitempty"`
}

type LocalServerFound struct {
	Port    int    `json:"port"`
	Address string `json:"address"`

	Usual string `json:"usual"`

	Models []string `json:"models,omitempty"`

	NeedsKey bool `json:"needs_key,omitempty"`

	Entry string `json:"entry,omitempty"`
}

type LocalServerRequest struct {
	Port  int    `json:"port"`
	Model string `json:"model"`
}

func (s *Server) wsLocalLook(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.LookForLocalServers == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "looking for a local model server is not available")
		return
	}

	reqID := msg.RequestID
	s.offLoop(ctx, conn, reqID, h, func() {
		found := h.LookForLocalServers()
		if found == nil {
			found = []LocalServerFound{}
		}
		s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "local_found", LocalFound: found})
	})
}

func (s *Server) wsLocalAdd(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.AddLocalServer == nil || msg.LocalServer == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "adding a local model server is not available")
		return
	}

	reqID, port, model := msg.RequestID, msg.LocalServer.Port, msg.LocalServer.Model
	s.offLoop(ctx, conn, reqID, h, func() {
		if err := h.AddLocalServer(port, model); err != nil {
			s.sendErrorFor(ctx, conn, reqID, fmt.Sprintf("local model: %v", err))
			return
		}

		if h.GetProviders != nil {
			d := h.GetProviders()
			s.sendMsg(ctx, conn, providersMessage(reqID, d))
			s.fanOut(context.Background(), s.conns(func(c carrier, _ *wsClient) bool { return c != conn }), providersMessage("", d))
		}
		if h.GetConfig != nil {
			s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "config", Config: h.GetConfig()})
		}
		s.BroadcastStatus()
		s.BroadcastConfig()
	})
}
