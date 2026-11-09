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
				s.broadcastAsksIfNaming(want)
				return
			}
			s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "config", Config: state})
			s.BroadcastConfig()
			s.BroadcastStatus()
			s.broadcastAsksIfNaming(want)
		})
		return
	}
	state, err := h.SetConfig(msg.Config)
	if err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
		s.broadcastAsksIfNaming(msg.Config)
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "config", Config: state})

	s.BroadcastConfig()
	s.BroadcastStatus()
	s.broadcastAsksIfNaming(msg.Config)
}

func (s *Server) broadcastAsksIfNaming(cfg map[string]any) {
	if _, named := cfg["speech.operator_speakers"]; named {
		s.BroadcastAsks()
	}
}

func (s *Server) wsToolToggle(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.SetToolFunc == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
		return
	}
	if msg.Tool == "" {
		s.sendErrorFor(ctx, conn, msg.RequestID, "tool_toggle requires tool")
		return
	}
	if err := h.SetToolFunc(msg.Tool, msg.Enabled); err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
		return
	}
	tools := h.GetTools()
	s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "tools", Tools: tools})
	others := s.conns(func(c carrier, _ *wsClient) bool { return c != conn })
	s.fanOut(ctx, others, ServerMessage{Type: "tools", Tools: tools})
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

const notPrivateRoute = "this connection isn't private, so the route wasn't changed. Use the AII OS computer, or turn on \"Serve over HTTPS\" in Settings → Access"

func changesSubstrate(cfg map[string]any) bool {
	if cfg == nil {
		return false
	}
	_, provider := cfg["llm.provider"]
	_, model := cfg["llm.model"]
	return provider || model
}

func (s *Server) wsThemeSet(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.SetThemeTokens == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
		return
	}
	if len(msg.Tokens) == 0 {
		s.sendErrorFor(ctx, conn, msg.RequestID, "theme_set requires tokens")
		return
	}
	if err := h.SetThemeTokens(msg.Tokens); err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "theme", Theme: s.themeBytes()})
	others := s.conns(func(c carrier, _ *wsClient) bool { return c != conn })
	s.fanOut(ctx, others, s.themeMessage())
}
