package dashboard

import (
	"context"
	"fmt"
	"strings"
)

func (s *Server) wsRestart(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.Restart == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
		return
	}
	if err := h.Restart(); err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "restart"})
}

func (s *Server) wsConfigSet(ctx context.Context, conn carrier, cl *wsClient, msg ClientMessage) {
	h := s.currentHandler()

	if h.SetConfig == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
		return
	}
	if msg.Config == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "config_set requires config")
		return
	}

	if changesRoute(msg.Config) && !cl.secretsOK {
		s.sendErrorFor(ctx, conn, msg.RequestID, notPrivateRoute)
		return
	}

	if changesSubstrate(msg.Config) || changesSpeech(msg.Config) {
		reqID, want := msg.RequestID, msg.Config
		s.offLoop(ctx, conn, reqID, h, func() {
			state, err := h.SetConfig(want)
			if err != nil {
				s.sendErrorFor(ctx, conn, reqID, err.Error())
				return
			}
			s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "config", Config: state})
			s.BroadcastStatus()
		})
		return
	}
	state, err := h.SetConfig(msg.Config)
	if err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "config", Config: state})

	s.BroadcastStatus()
}

func (s *Server) wsToolToggle(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.SetToolFunc == nil {
		s.sendError(ctx, conn, "not available")
		return
	}
	if msg.Tool == "" {
		s.sendError(ctx, conn, "tool_toggle requires tool")
		return
	}
	if err := h.SetToolFunc(msg.Tool, msg.Enabled); err != nil {
		s.sendError(ctx, conn, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "tools", Tools: h.GetTools()})
}

func (s *Server) wsUpdateCheck(ctx context.Context, conn carrier, msg ClientMessage) {

	h := s.currentHandler()
	if h.UpdateCheck == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "updates are not available")
		return
	}
	st, err := h.UpdateCheck()
	if err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, fmt.Sprintf("update check: %v", err))
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "update_check", Update: st})
}

func (s *Server) wsUninstall(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.Uninstall == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "uninstall is not offered here")
		return
	}
	if err := h.Uninstall(); err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "uninstall"})
}

func changesSpeech(cfg map[string]any) bool {
	for key := range cfg {
		if strings.HasPrefix(key, "speech.stt.") || strings.HasPrefix(key, "speech.tts.") {
			return true
		}
	}
	return false
}

func changesRoute(cfg map[string]any) bool {
	_, mode := cfg["certificate.route_mode"]
	_, relay := cfg["certificate.relay_endpoint"]
	_, addresses := cfg["certificate.direct_addresses"]
	return mode || relay || addresses
}

const notPrivateRoute = "this connection is not private, so the route was not changed — open the dashboard on the machine itself, or serve it over HTTPS (Settings → Dashboard), and choose the route there"

func changesSubstrate(cfg map[string]any) bool {
	if cfg == nil {
		return false
	}
	_, provider := cfg["llm.provider"]
	_, model := cfg["llm.model"]
	return provider || model
}
