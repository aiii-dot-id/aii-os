package dashboard

import (
	"context"
	"errors"
	"fmt"
)

func (s *Server) wsSandboxChange(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.ChangeSandbox == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
		return
	}
	if err := h.ChangeSandbox(msg.Root, msg.Type == "sandbox_add"); err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())

		if h.GetSandbox != nil {
			s.sendSandbox(ctx, conn, h, "")
		}
		return
	}
	s.sendSandbox(ctx, conn, h, msg.RequestID)
	s.broadcastSandbox(ctx, conn)
}

func (s *Server) wsSandboxSwitch(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.SetSandboxOff == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
		return
	}
	if msg.SandboxOff == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "sandbox_switch needs sandbox_off: true or false; nothing was changed")
		return
	}
	if err := h.SetSandboxOff(*msg.SandboxOff, s.signedIn(conn)); err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
	} else {
		s.sendSandbox(ctx, conn, h, msg.RequestID)
	}
	if h.GetSandbox == nil {
		return
	}
	if sb, err := h.GetSandbox(); err == nil {
		s.fanOut(ctx, s.conns(func(c carrier, _ *wsClient) bool { return c != conn }), ServerMessage{Type: "sandbox", Sandbox: sb})
	}
}

func (s *Server) wsAgentNotesCopy(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.CopyAgentNotes == nil || h.GetSandbox == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
		return
	}
	said, err := h.CopyAgentNotes(ctx)
	sb, readErr := h.GetSandbox()
	switch {
	case err != nil:
		var refused *AgentNotesRefusal
		code := ""
		if errors.As(err, &refused) {
			code = refused.Code
		}
		s.sendRequestError(ctx, conn, msg.RequestID, code, err.Error())
		if readErr == nil {
			s.sendMsg(ctx, conn, ServerMessage{Type: "sandbox", Sandbox: sb})
		}
	case readErr != nil:
		s.sendErrorFor(ctx, conn, msg.RequestID, said+fmt.Sprintf(sandboxNotReadAgain, readErr))
	default:
		s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "sandbox", Sandbox: sb, Message: said})
	}
	if readErr == nil {
		s.fanOut(ctx, s.conns(func(c carrier, _ *wsClient) bool { return c != conn }), ServerMessage{Type: "sandbox", Sandbox: sb})
	}
}

const sandboxNotReadAgain = " (The sandbox could not be read again: %v.)"

func (s *Server) wsProject(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()

	if h.ProjectAct == nil || msg.Project == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
		return
	}
	p := msg.Project
	if err := h.ProjectAct(*p); err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
		return
	}
	s.sendProjects(ctx, conn, h, msg.RequestID)

	s.broadcastProjects(ctx, h, conn)
}

func (s *Server) wsGrade(ctx context.Context, conn carrier, msg ClientMessage) {

	h := s.currentHandler()
	if msg.Grade == nil || h.GradeResult == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "grading is not available")
		return
	}
	if _, err := h.GradeResult(*msg.Grade); err != nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
		return
	}
	if h.GetWork != nil {
		if w, werr := h.GetWork(); werr == nil {
			s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "work", Work: w})
		}
	}
}
