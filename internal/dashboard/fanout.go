package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/coder/websocket"
)

func (s *Server) PokeOutbox() {
	select {
	case s.outboxSignal <- struct{}{}:
	default:
	}
}

func (s *Server) SetQuiesceGate(g *quiesce.Gate) { s.gate = g }

func (s *Server) runOutboxPump(ctx context.Context) {
	defer close(s.pumpDone)
	sweep := quiesce.NewTicker(s.gate, s.sweepEvery)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.outboxSignal:
		case <-sweep.C:
		}

		select {
		case <-s.outboxSignal:
		default:
		}

		h := s.currentHandler()
		if h == nil || h.TakeOutbox == nil {
			continue
		}

		conns := s.conns(nil)
		if len(conns) == 0 {

			continue
		}
		s.carryOutbox(ctx, h, conns, false)
	}
}

func (s *Server) carryOutbox(ctx context.Context, h *WSHandler, conns []carrier, answer bool) {
	if h.TakeOutbox == nil {
		return
	}
	items, settle, err := h.TakeOutbox()
	if err != nil {
		return
	}
	shown := false
	if len(items) > 0 || answer {
		shown = s.fanOut(ctx, conns, ServerMessage{Type: "outbox", Outbox: items})
	}
	settle(shown)
}

func (s *Server) PushTransient(id, content string) int {
	return s.PushTransientItems([]OutboxItem{{ID: id, To: "operator", Content: content}})
}

func (s *Server) PushTransientItems(items []OutboxItem) int {
	msg := ServerMessage{Type: "outbox", Outbox: items}
	conns := s.conns(nil)

	s.fanOut(context.Background(), conns, msg)
	return len(conns)
}

func (s *Server) sendStatus(ctx context.Context, conn carrier, h *WSHandler) {
	if msg, ok := s.statusMessage(h); ok {
		s.sendMsg(ctx, conn, msg)
	}
}

func (s *Server) statusMessage(h *WSHandler) (ServerMessage, bool) {
	if h == nil || h.GetStats == nil {
		return ServerMessage{}, false
	}
	stats, err := h.GetStats()
	if err != nil {
		return ServerMessage{}, false
	}
	stats.Uptime = time.Since(s.startedAt).Round(time.Second).String()

	stats.Voice = h.HearUtterance != nil && h.VoiceConfigured != nil && h.VoiceConfigured()
	if stats.Voice {
		stats.VoiceMaxFrameBytes = maxVoiceFrameBytes
	}
	stats.VoiceEngine = h.VoiceEngine != nil && h.VoiceSessionOpen != nil && h.AudioPlane != nil && h.VoiceEngine()
	if h.SpeakerPolicy != nil {
		stats.Speakers = h.SpeakerPolicy()
	}
	stats.VoiceState = "setup"
	if h.VoiceStatus != nil {
		stats.VoiceState, stats.VoiceReason, stats.VoiceSource = h.VoiceStatus()
	}
	if h.VoiceMode != nil {
		stats.VoiceListen, stats.VoiceSpeak, stats.VoiceModeRevision = h.VoiceMode()
	}

	if h.ReplyVoice != nil && h.SpeakMint != nil {
		stats.ReplyVoice = h.ReplyVoice()
	}
	if h.VoiceSpeaker != nil {
		stats.VoiceSpeaker = h.VoiceSpeaker()
	}
	if (stats.VoiceState == "plugin" && !stats.VoiceEngine) || (stats.VoiceState == "cloud" && h.HearUtterance == nil) {
		stats.VoiceState, stats.VoiceReason, stats.VoiceSource = "setup", "", ""
	}
	return ServerMessage{Type: "status", Stats: stats}, true
}

func (s *Server) BroadcastStatus() {
	if msg, ok := s.statusMessage(s.currentHandler()); ok {
		s.broadcast(msg)
	}
}

func (s *Server) steeringMessage(h *WSHandler) ServerMessage {
	if h == nil || h.PendingSteers == nil {
		return ServerMessage{Type: "steering"}
	}
	return ServerMessage{Type: "steering", Pending: h.PendingSteers()}
}

func (s *Server) BroadcastResponse(role, message string) {
	s.broadcast(s.spokenAloud(ServerMessage{Type: "response", Message: message, Role: role, Done: true}))
}

func (s *Server) BroadcastSteering() { s.broadcast(s.steeringMessage(s.currentHandler())) }

func (s *Server) sendOutbox(ctx context.Context, conn carrier, h *WSHandler) {
	s.carryOutbox(ctx, h, []carrier{conn}, true)
}

func (s *Server) sendSandbox(ctx context.Context, conn carrier, h *WSHandler, requestID string) {
	if h.GetSandbox == nil {
		s.sendErrorFor(ctx, conn, requestID, "not available")
		return
	}
	sb, err := h.GetSandbox()
	if err != nil {
		s.sendErrorFor(ctx, conn, requestID, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{RequestID: requestID, Type: "sandbox", Sandbox: sb})
}

func (s *Server) sendWorkspace(ctx context.Context, conn carrier, h *WSHandler, id string) {
	if h.GetWorkspace == nil {
		s.sendError(ctx, conn, "not available")
		return
	}
	if id == "" {
		s.sendError(ctx, conn, "workspace query requires a project id")
		return
	}
	ws, err := h.GetWorkspace(id)
	if err != nil {
		s.sendError(ctx, conn, err.Error())
		return
	}
	if ws == nil {

		s.sendError(ctx, conn, "project not found: "+id)
		return
	}

	s.wsMu.Lock()
	if cl := s.wsConns[conn]; cl != nil {
		cl.viewing = id
	}
	s.wsMu.Unlock()
	s.sendMsg(ctx, conn, ServerMessage{Type: "workspace", Workspace: ws})
}

func (s *Server) sendProjects(ctx context.Context, conn carrier, h *WSHandler, reqID string) {
	if h.GetProjects == nil {
		s.sendErrorFor(ctx, conn, reqID, "not available")
		return
	}
	ps, err := h.GetProjects()
	if err != nil {
		s.sendErrorFor(ctx, conn, reqID, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "projects", Projects: ps})
}

func (s *Server) broadcastProjects(ctx context.Context, h *WSHandler, skip carrier) {
	if h.GetProjects == nil {
		return
	}
	ps, err := h.GetProjects()
	if err != nil {
		return
	}
	msg := ServerMessage{Type: "projects", Projects: ps}
	s.fanOut(ctx, s.conns(func(c carrier, _ *wsClient) bool { return c != skip }), msg)
}

func (s *Server) BroadcastConfig() {
	h := s.currentHandler()
	if h == nil || h.GetConfig == nil {
		return
	}
	state := h.GetConfig()
	if state == nil {
		return
	}
	s.broadcast(ServerMessage{Type: "config", Config: state})
}

func (s *Server) BroadcastAsks() {
	h := s.currentHandler()
	if h == nil || h.GetAsks == nil {
		return
	}
	asks := h.GetAsks()
	if asks == nil {
		asks = []AskView{}
	}
	s.broadcast(ServerMessage{Type: "asks", Asks: asks})
}

func (s *Server) BroadcastSystemLine(text string) {
	s.broadcast(ServerMessage{Type: "system_line", Message: text})
}

func providersMessage(requestID string, d ProviderDirectory) ServerMessage {
	skip := d.SkipSignInWithValidToken
	return ServerMessage{RequestID: requestID, Type: "providers", Providers: d.Providers,
		BrokenProviders: d.Broken, SkipSignInWithValidToken: &skip}
}

func (s *Server) replyConfig(ctx context.Context, conn carrier, reqID string, h *WSHandler) {
	var state *ConfigState
	if h.GetConfig != nil {
		state = h.GetConfig()
	}
	s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "config", Config: state})
}

func (s *Server) replyProviders(ctx context.Context, conn carrier, reqID string, h *WSHandler) {
	if h.GetProviders != nil {
		s.sendMsg(ctx, conn, providersMessage(reqID, h.GetProviders()))
	}
}

func (s *Server) BroadcastProviders() {
	h := s.currentHandler()
	if h == nil || h.GetProviders == nil {
		return
	}
	s.broadcast(providersMessage("", h.GetProviders()))
}

func (s *Server) BroadcastProjects() {
	h := s.currentHandler()
	if h == nil || h.GetProjects == nil {
		return
	}
	ps, err := h.GetProjects()
	if err != nil {
		return
	}
	s.broadcast(ServerMessage{Type: "projects", Projects: ps})
}

func (s *Server) BroadcastWork() {
	h := s.currentHandler()
	if h == nil || h.GetWork == nil {
		return
	}
	w, err := h.GetWork()
	if err != nil {
		return
	}
	s.broadcast(ServerMessage{Type: "work", Work: w})
}

func (s *Server) BroadcastEvent(kind, name, args string) {
	s.broadcast(ServerMessage{
		Type: "event", Kind: "tool_call", Name: name,
		Args: args, Role: "system",
	})
}

func (s *Server) PushWorkspace(id string, ws *WorkspaceState) {
	if ws == nil {
		return
	}
	msg := ServerMessage{Type: "workspace", Workspace: ws}
	s.fanOut(context.Background(), s.conns(func(_ carrier, cl *wsClient) bool { return cl.viewing == id }), msg)
}

func (s *Server) sendError(ctx context.Context, conn carrier, msg string) {
	s.sendMsg(ctx, conn, ServerMessage{Type: "error", Message: msg})
}

func (s *Server) sendErrorFor(ctx context.Context, conn carrier, requestID, msg string) {
	s.sendMsg(ctx, conn, ServerMessage{RequestID: requestID, Type: "error", Message: msg})
}

const writeWait = 2 * time.Second

func (s *Server) sendMsg(ctx context.Context, conn carrier, msg ServerMessage) error {
	if !s.fanOut(ctx, []carrier{conn}, msg) {
		return errNotSent
	}
	return nil
}

var errNotSent = errors.New("the frame did not reach the page")

func (s *Server) fanOut(ctx context.Context, conns []carrier, msg ServerMessage) bool {
	var frames [2][]byte
	delivered := false
	for _, conn := range conns {
		cl := s.client(conn)
		if cl == nil {
			continue
		}
		class := 0
		if msg.Config != nil && cl.secretsOK {
			class = 1
		}
		if frames[class] == nil {
			m := msg
			if m.Config != nil {
				stamped := *m.Config
				stamped.SecretsOK = cl.secretsOK
				m.Config = &stamped
			}
			data, err := json.Marshal(m)
			if err != nil {

				logsink.Warn("dashboard.error", "a %q message could not be encoded (%v); nothing was sent", msg.Type, err)
				return delivered
			}
			frames[class] = data
		}
		wctx, cancel := context.WithTimeout(ctx, writeWait)
		cl.mu.Lock()
		err := conn.Write(wctx, websocket.MessageText, frames[class])
		cl.mu.Unlock()
		cancel()
		if err != nil {

			logsink.Warn("dashboard.error", "write failed (%v) — dropping client", err)
			s.dropConn(conn)
			continue
		}
		delivered = true
	}
	return delivered
}

func (s *Server) dropConn(conn carrier) {
	if s.forgetConn(conn) {

		conn.CloseNow()
	}
}
