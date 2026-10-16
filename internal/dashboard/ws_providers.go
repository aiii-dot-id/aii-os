package dashboard

import (
	"context"
	"fmt"
)

func (s *Server) wsSignInCancel(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	cancel, name := h.CancelSignIn, msg.Provider
	if msg.Type == "profile_signin_cancel" {
		cancel, name = h.CancelProfileSignIn, msg.Profile
	}
	if cancel != nil {
		if err := cancel(name); err != nil {
			s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
		}
	}
}

func (s *Server) wsProviderSignIn(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.SignInProvider == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "sign-in not available")
		return
	}
	reqID, name := msg.RequestID, msg.Provider
	s.offLoop(ctx, conn, reqID, h, func() {
		u, err := h.SignInProvider(name)
		if err != nil {
			s.sendErrorFor(ctx, conn, reqID, fmt.Sprintf("sign-in: %v", err))
			return
		}
		s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "provider_signin", SignInURL: u})
	})
}

func (s *Server) wsProviderSignInComplete(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.CompleteSignIn == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "sign-in not available")
		return
	}
	if err := h.CompleteSignIn(msg.Provider, msg.Input); err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, fmt.Sprintf("sign-in: %v", err))
		return
	}
	s.replyProviders(ctx, conn, msg.RequestID, h)
}

func (s *Server) wsEffortSet(ctx context.Context, conn carrier, msg ClientMessage) {

	h := s.currentHandler()
	if h.SetEffort == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "effort: not available")
		return
	}
	reqID, level := msg.RequestID, msg.Effort
	s.offLoop(ctx, conn, reqID, h, func() {
		if err := h.SetEffort(level); err != nil {
			s.sendErrorFor(ctx, conn, reqID, fmt.Sprintf("effort: %v", err))
			return
		}
		s.replyProviders(ctx, conn, reqID, h)
		if h.GetConfig != nil {
			s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "config", Config: h.GetConfig()})
		}

		s.BroadcastConfig()
	})
}

func (s *Server) wsProviderEdit(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	var err error
	switch {
	case msg.Type == "provider_set" && h.SetProvider != nil && msg.Entry != nil:
		err = h.SetProvider(*msg.Entry)
	case msg.Type == "provider_delete" && h.DeleteProvider != nil:
		err = h.DeleteProvider(msg.Provider)
	case msg.Type == "provider_repair" && h.RepairProvider != nil && msg.Position != nil:
		err = h.RepairProvider(*msg.Position, msg.EntrySHA256)
	case msg.Type == "provider_remove_broken" && h.RemoveBrokenProvider != nil && msg.Position != nil:
		err = h.RemoveBrokenProvider(*msg.Position, msg.EntrySHA256)
	case msg.Type == "speech_service" && h.SetSpeechService != nil:
		err = h.SetSpeechService(msg.Provider, msg.APIKey, msg.BaseURL)
	default:
		err = fmt.Errorf("provider editing not available")
	}
	if err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, fmt.Sprintf("providers: %v", err))
		return
	}
	s.replyProviders(ctx, conn, msg.RequestID, h)
	if h.GetConfig != nil {
		s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "config", Config: h.GetConfig()})
	}

	s.BroadcastStatus()
	s.BroadcastConfig()
}
