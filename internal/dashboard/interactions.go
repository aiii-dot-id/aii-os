package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/coder/websocket"
)

func (s *Server) PokeInteractions() {
	select {
	case s.interactionSignal <- struct{}{}:
	default:
	}
}
func (s *Server) runInteractionView(ctx context.Context) {
	defer close(s.interactionDone)
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.interactionSignal:
			s.broadcast(ServerMessage{Type: "interaction_changed"})
		}
	}
}
func (s *Server) sendInteractionQuery(ctx context.Context, c *websocket.Conn, h *WSHandler, msg ClientMessage) {
	if len(msg.RequestID) > 256 {
		s.interactionError(ctx, c, "", interaction.Invalid("request ID exceeds 256 bytes"))
		return
	}
	if h == nil || h.Interactions == nil {
		s.interactionError(ctx, c, msg.RequestID, &interaction.Error{Code: "INTERACTION_SOURCE_UNAVAILABLE", Detail: "history unavailable"})
		return
	}

	if msg.InteractionQuery == nil {
		s.interactionError(ctx, c, msg.RequestID, interaction.Invalid("interaction query required"))
		return
	}
	cl := s.client(c)
	if cl == nil {
		return
	}
	if cl.interactionRead.CompareAndSwap(false, true) {
		if !s.runJob(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case request := <-cl.interactionRequests:
					current := s.currentHandler()
					if current == nil || current.Interactions == nil {
						s.interactionError(ctx, c, request.RequestID, &interaction.Error{Code: "INTERACTION_SOURCE_UNAVAILABLE", Detail: "history unavailable"})
						continue
					}
					q := *request.InteractionQuery
					q.Window = request.InteractionWindow
					page, err := current.Interactions.QueryInteractions(ctx, q)
					if current != s.currentHandler() {
						err = &interaction.Error{Code: "INTERACTION_SOURCE_CHANGED", Detail: "history owner replaced"}
					}
					if err != nil {
						s.interactionError(ctx, c, request.RequestID, err)
					} else {
						s.sendMsg(ctx, c, ServerMessage{Type: "interaction_page", RequestID: request.RequestID, InteractionPage: page, InteractionWindow: page.Window})
					}
				}
			}
		}) {
			cl.interactionRead.Store(false)
			s.interactionError(ctx, c, msg.RequestID, &interaction.Error{Code: "INTERACTION_SOURCE_UNAVAILABLE", Detail: "host stopping"})
			return
		}
	}

	select {
	case cl.interactionRequests <- msg:
	default:
		s.interactionError(ctx, c, msg.RequestID, &interaction.Error{Code: "INTERACTION_READ_BUSY", Detail: "history read already queued"})
	}
}

func (s *Server) interactionError(ctx context.Context, c *websocket.Conn, id string, err error) {
	reason := "INTERACTION_SOURCE_UNAVAILABLE"
	var known *interaction.Error
	if errors.As(err, &known) {
		reason = known.Code
	}
	s.sendMsg(ctx, c, ServerMessage{Type: "interaction_error", RequestID: id, Message: err.Error(), InteractionReason: reason})
}

func (s *Server) serveInteractionDetail(w http.ResponseWriter, r *http.Request) {
	s.jobsMu.Lock()
	if s.jobsStopping {
		s.jobsMu.Unlock()
		http.Error(w, "host stopping", http.StatusServiceUnavailable)
		return
	}
	s.jobsWG.Add(1)
	s.jobsMu.Unlock()
	defer s.jobsWG.Done()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !s.tokenAuthorized(r) {
		http.Error(w, "dashboard access token required", http.StatusUnauthorized)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameSiteAs(origin, r.Host) {
		http.Error(w, "foreign origin", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h := s.currentHandler()
	if h == nil || h.Interactions == nil {
		http.Error(w, "history unavailable", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	rawOffset := q.Get("offset")
	if rawOffset == "" {
		rawOffset = "0"
	}
	offset, err := strconv.ParseInt(rawOffset, 10, 64)
	if err != nil {
		http.Error(w, "invalid offset", http.StatusBadRequest)
		return
	}
	req := interaction.ReadRequest{Version: interaction.Version, Source: q.Get("source"), ID: q.Get("id"), Incarnation: q.Get("incarnation"), SHA256: q.Get("sha256"), Offset: offset, Length: interaction.MaxReadBytes}
	result, err := h.Interactions.ReadInteraction(r.Context(), req)
	if h != s.currentHandler() {
		err = &interaction.Error{Code: "INTERACTION_SOURCE_CHANGED", Detail: "history owner replaced"}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
