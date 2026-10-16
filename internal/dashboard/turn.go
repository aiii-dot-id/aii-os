package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func (s *Server) startTurnLifecycles() {
	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	turnCtx, turnCancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.pumpCancel = pumpCancel
	s.turnCtx = turnCtx
	s.turnCtxCancel = turnCancel
	s.mu.Unlock()
	go s.runOutboxPump(pumpCtx)
	go s.runInteractionView(pumpCtx)
}

func (s *Server) serverTurnCtx() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.turnCtx != nil {
		return s.turnCtx
	}
	return context.Background()
}

func (s *Server) runOperatorMessage(ctx context.Context, conn carrier, h *WSHandler, message string, recorded func(ChatRecording), requestIDs ...string) {
	s.runOperatorRequest(ctx, conn, h, message, nil, recorded, requestIDs...)
}

func (s *Server) runOperatorRequest(ctx context.Context, conn carrier, h *WSHandler, message string, options *conversation.RequestOptions, recorded func(ChatRecording), requestIDs ...string) {
	turnCtx := s.serverTurnCtx()
	receiptID := ""
	if len(requestIDs) > 0 {
		receiptID = requestIDs[0]
	}
	if receiptID != "" || recorded != nil {
		turnCtx = withChatRecording(turnCtx, func(r ChatRecording) {
			if recorded != nil {
				recorded(r)
			}
			if receiptID != "" {
				s.sendMsg(ctx, conn, ServerMessage{Type: "chat_receipt", RequestID: receiptID, ChatRecording: &r})
			}
		})
	}
	ack := func(state string) {
		if len(requestIDs) > 0 && requestIDs[0] != "" {
			s.sendMsg(ctx, conn, ServerMessage{Type: "chat_ack", RequestID: requestIDs[0], Message: state})
		}
	}

	refuse := func(message string) {
		id := ""
		if len(requestIDs) > 0 {
			id = requestIDs[0]
		}
		if options != nil {

			s.sendRequestError(ctx, conn, id, "REQUEST_NOT_ADMITTED", message)
			return
		}
		s.sendErrorFor(ctx, conn, id, message)
	}
	if options != nil {
		id := ""
		if len(requestIDs) > 0 {
			id = requestIDs[0]
		}
		var err error
		if id == "" {
			err = errors.New("constrained requests require request_id")
		} else {
			var admitted context.Context
			admitted, err = conversation.WithRequestOptions(turnCtx, *options)
			if err == nil {
				turnCtx = admitted
			}
		}
		if err != nil {
			ChatNotRecorded(turnCtx, err.Error())
			ack("refused")
			s.sendRequestError(ctx, conn, id, "REQUEST_OPTIONS_INVALID", err.Error())
			return
		}
		turnCtx = context.WithValue(turnCtx, requestIDKey{}, id)
		if h == nil || h.AdmitRequest == nil || h.ReleaseTurn == nil {
			ChatNotRecorded(turnCtx, "request options are not supported by this handler")
			ack("refused")
			s.sendRequestError(ctx, conn, id, "REQUEST_OPTIONS_UNSUPPORTED", "request options are not supported by this handler")
			return
		}

		copyHandler := *h
		admitRequest := h.AdmitRequest
		copyHandler.AdmitChat = func(c context.Context, text string) (bool, error) { return false, admitRequest(c, text) }
		h = &copyHandler
	}
	if h == nil {
		ChatNotRecorded(turnCtx, "identity is not ready")
		ack("refused")
		refuse("identity is not ready")
		return
	}

	if paths := Attachments(ctx); len(paths) > 0 {
		told, why := s.admittedAttachments(h, paths)
		if why != "" {
			ChatNotRecorded(turnCtx, why)
			ack("refused")
			refuse(why)
			return
		}
		turnCtx = WithAttachments(turnCtx, told)
	}

	admit := h.AdmitChat
	if admit == nil && h.Steer != nil {
		admit = func(_ context.Context, text string) (bool, error) { return h.Steer(text) }
	}
	if admit != nil {
		delivered, err := admit(turnCtx, message)
		switch {
		case options != nil && err != nil:

			code := "REQUEST_NOT_ADMITTED"
			var invalid *conversation.RequestOptionsError
			if errors.As(err, &invalid) {
				code = "REQUEST_OPTIONS_INVALID"
			}
			ChatNotRecorded(turnCtx, err.Error())
			ack("refused")
			s.sendRequestError(ctx, conn, requestIDs[0], code, err.Error())
			return
		case errors.Is(err, ErrBusyInternal):
			if h.HandleMessage == nil {
				ChatNotRecorded(turnCtx, "identity is not ready for queued turns")
				ack("refused")
				return
			}

			s.broadcast(ServerMessage{Type: "response", Role: "system", Done: true,
				Message: "An internal pass (memory metabolism) holds the turn — your message is queued and will run the moment it ends."})
			qtext := message
			if !s.runJob(func() {
				actx, acancel := context.WithTimeout(turnCtx, queuedTurnMaxWait)
				defer acancel()
				if h.AcquireTurn == nil {
					ChatNotRecorded(turnCtx, "identity not ready for queued turns")
					refuse("identity not ready for queued turns")
					return
				}
				if err := h.AcquireTurn(actx); err != nil {
					ChatNotRecorded(turnCtx, "queued message could not acquire a turn: "+err.Error())
					refuse(fmt.Sprintf("the internal pass did not yield in time; your message was NOT delivered — please send it again (%v)", err))
					return
				}
				s.handleChat(turnCtx, conn, qtext, h)
				s.sendStatus(ctx, conn, h)
				logsink.Info("dashboard.end", "queued turn finished (%d chars in)", len(qtext))
			}) {
				ChatNotRecorded(turnCtx, "identity is stopping; queued message did not start")
				refuse("identity is stopping; your message was NOT delivered")
				ack("refused")
				return
			}
			ack("accepted")
			return
		case err != nil:
			ChatNotRecorded(turnCtx, err.Error())
			refuse(err.Error())
			ack("refused")
			return
		case delivered:
			ack("accepted")
			s.sendMsg(ctx, conn, ServerMessage{Type: "steered", Message: message})

			s.BroadcastSteering()
			return
		}
	}
	if h.HandleMessage == nil {
		if h.AdmitChat != nil && h.ReleaseTurn != nil {
			h.ReleaseTurn()
		}
		ChatNotRecorded(turnCtx, "identity is not ready")
		ack("refused")
		refuse("identity is not ready")
		return
	}

	text := message
	if !s.runJob(func() {
		s.handleChat(turnCtx, conn, text, h)
		s.sendStatus(ctx, conn, h)
		logsink.Info("dashboard.end", "turn finished (%d chars in)", len(text))
	}) {
		if h.AdmitChat != nil && h.ReleaseTurn != nil {
			h.ReleaseTurn()
		}
		ChatNotRecorded(turnCtx, "identity is stopping; message did not start")
		refuse("identity is stopping; your message was NOT delivered")
		ack("refused")
		return
	}
	ack("accepted")
}

func (s *Server) handleChat(ctx context.Context, conn carrier, message string, h *WSHandler) {

	defer ChatRecorded(ctx, nil, errors.New("the host did not confirm whether your message was recorded"))
	if h == nil || h.HandleMessage == nil {
		ChatNotRecorded(ctx, "identity not ready")
		s.sendError(ctx, conn, "identity not ready")
		return
	}

	speaker := h.Speaker
	if speaker == "" {
		speaker = "system"
	}
	if speaker == "identity" {

		s.broadcast(ServerMessage{Type: "response", Message: "", Stream: true})
	}

	var output json.RawMessage
	ctx = context.WithValue(ctx, structuredOutputKey{}, func(raw json.RawMessage) { output = append(json.RawMessage(nil), raw...) })
	var response string
	var err error
	if h.ObserveChat != nil {

		emit := func(kind, name, args string) {
			s.broadcast(ServerMessage{Type: "event", Kind: kind, Name: name, Args: args})
		}
		response, err = h.ObserveChat(ctx, message, emit)
	} else {
		response, err = h.HandleMessage(ctx, message)
	}

	if err == nil {
		if options, constrained := conversation.RequestOptionsFromContext(ctx); constrained && options.ResponseFormat != nil {
			err = llm.ValidateStructuredOutput(options.ResponseFormat, string(output))
		}
	}
	if err != nil {

		if response != "" {
			s.broadcast(s.spokenAloud(ServerMessage{Type: "response", RequestID: requestID(ctx), RequestErrorCode: requestFailureCode(ctx, err), Message: response, Role: speaker, Done: true}))
		}

		if errors.Is(err, context.Canceled) {

			if id := requestID(ctx); id != "" {
				s.broadcast(ServerMessage{Type: "error", RequestID: id, RequestErrorCode: requestFailureCode(ctx, err), Message: "the turn was stopped before the request completed"})
			}
			return
		}
		s.broadcast(ServerMessage{Type: "error", RequestID: requestID(ctx), RequestErrorCode: requestFailureCode(ctx, err), Message: fmt.Sprintf("identity error: %v", err)})
		return
	}

	s.broadcast(s.spokenAloud(ServerMessage{Type: "response", RequestID: requestID(ctx), StructuredOutput: output, Message: response, Role: speaker, Done: true}))
}
