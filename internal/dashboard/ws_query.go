package dashboard

import (
	"context"
	"strings"
)

func (s *Server) queryHistory(ctx context.Context, conn carrier, h *WSHandler, msg ClientMessage) {
	if h.RecentTurns != nil {
		if turns, err := h.RecentTurns(); err == nil && len(turns) > 0 {
			s.sendMsg(ctx, conn, ServerMessage{Type: "history", RequestID: msg.RequestID, History: turns})
		} else if err != nil {
			s.sendMsg(ctx, conn, ServerMessage{Type: "error", RequestID: msg.RequestID, Message: "recent conversation unavailable: " + err.Error()})
		}
	}
}

func (s *Server) queryWork(ctx context.Context, conn carrier, h *WSHandler) {
	if h.GetWork == nil {
		s.sendError(ctx, conn, "not available")
		return
	}
	if w, err := h.GetWork(); err == nil {
		s.sendMsg(ctx, conn, ServerMessage{Type: "work", Work: w})
	} else {
		s.sendError(ctx, conn, err.Error())
	}
}

func (s *Server) queryTools(ctx context.Context, conn carrier, h *WSHandler) {
	if h.GetTools == nil {
		s.sendError(ctx, conn, "not available")
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "tools", Tools: h.GetTools()})
}

func (s *Server) queryRecall(ctx context.Context, conn carrier, h *WSHandler, msg ClientMessage) {
	if h.Recall == nil {
		s.sendError(ctx, conn, "not available")
		return
	}
	q := strings.TrimSpace(msg.Q)
	if q == "" {
		s.sendError(ctx, conn, "recall needs a word or phrase")
		return
	}
	text, err := h.Recall(q)
	if err != nil {
		s.sendError(ctx, conn, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "recall", Query: q, Message: text, RequestID: msg.RequestID})
}

func (s *Server) queryIdentity(ctx context.Context, conn carrier, h *WSHandler) {
	if h.GetIdentity == nil {
		s.sendError(ctx, conn, "not available")
		return
	}
	state, err := h.GetIdentity()
	if err != nil {
		s.sendError(ctx, conn, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "identity", Identity: state})
}

func (s *Server) queryConfig(ctx context.Context, conn carrier, h *WSHandler) {
	if h.GetConfig == nil {
		s.sendError(ctx, conn, "not available")
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "config", Config: h.GetConfig()})
}

func (s *Server) queryAsks(ctx context.Context, conn carrier, h *WSHandler) {
	asks := []AskView{}
	if h.GetAsks != nil {
		if got := h.GetAsks(); got != nil {
			asks = got
		}
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "asks", Asks: asks})
}

func (s *Server) queryProviders(ctx context.Context, conn carrier, h *WSHandler) {
	if h.GetProviders == nil {
		s.sendMsg(ctx, conn, ServerMessage{Type: "providers", Providers: nil})
		return
	}
	s.sendMsg(ctx, conn, providersMessage("", h.GetProviders()))
}

func (s *Server) queryDiscover(ctx context.Context, conn carrier, h *WSHandler, msg ClientMessage) {
	if h.DiscoverModels == nil {
		s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "error", Message: "not available", Provider: msg.Provider})
		return
	}

	reqID, provider, baseURL, apiKey := msg.RequestID, msg.Provider, msg.BaseURL, msg.APIKey
	s.offLoop(ctx, conn, reqID, h, func() {
		models, needsKey, err := h.DiscoverModels(provider, baseURL, apiKey)
		if err != nil {
			s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "error", Message: err.Error(), Provider: provider, NeedsKey: needsKey})
			return
		}
		s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "models", Provider: provider, ModelList: models})
	})
}

func (s *Server) querySpeechLists(ctx context.Context, conn carrier, h *WSHandler, msg ClientMessage) {
	if h.SpeechLists == nil {
		s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "error", Message: "not available", Provider: msg.Provider})
		return
	}

	reqID, provider, direction, search, language, apiKey := msg.RequestID, msg.Provider, msg.Direction, msg.Search, msg.Language, msg.APIKey
	s.offLoop(ctx, conn, reqID, h, func() {
		lists, err := h.SpeechLists(provider, direction, search, language, apiKey)
		if err != nil {
			s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "error", Message: err.Error(), Provider: provider})
			return
		}
		s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "speech_lists", SpeechLists: &lists})
	})
}

func (s *Server) queryDashboardToken(ctx context.Context, conn carrier, h *WSHandler, msg ClientMessage) {

	if h.DashboardToken == nil || !s.AccessTokenRequired() {
		s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "error", Message: "not available"})
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "dashboard_token", DashboardToken: h.DashboardToken()})
}

func (s *Server) queryContinuity(ctx context.Context, conn carrier, h *WSHandler) {
	if h.GetContinuity == nil {
		s.sendError(ctx, conn, "not available")
		return
	}
	cont, err := h.GetContinuity()
	if err != nil {
		s.sendError(ctx, conn, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "continuity", Continuity: cont})
}

func (s *Server) queryLogs(ctx context.Context, conn carrier, h *WSHandler, msg ClientMessage) {

	if h.ListLogs == nil {
		s.sendMsg(ctx, conn, ServerMessage{Type: "logs", LogsList: nil})
		return
	}
	if msg.Name != "" {
		tail, err := h.TailLogs(msg.Name)
		if err != nil {
			s.sendError(ctx, conn, err.Error())
			return
		}
		s.sendMsg(ctx, conn, ServerMessage{Type: "logs", LogsTail: tail})
		return
	}
	files, err := h.ListLogs()
	if err != nil {
		s.sendError(ctx, conn, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "logs", LogsList: files})
}
